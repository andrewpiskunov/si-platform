package blob

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Config connects to an operator-provisioned private bucket. Empty AccessKey
// and SecretKey select the standard AWS credential chain. TempDir controls local
// upload staging; an empty value uses the operating system temporary directory.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string
	PathStyle                                                    bool
	TempDir                                                      string
}

type s3Store struct {
	client          *s3.Client
	bucket, tempDir string
}

func NewS3(ctx context.Context, cfg S3Config) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Bucket == "" || strings.ContainsAny(cfg.Bucket, "/\\\r\n\x00 ") || (cfg.AccessKey == "") != (cfg.SecretKey == "") || (cfg.SessionToken != "" && cfg.AccessKey == "") {
		return nil, ErrStorage
	}
	if cfg.Endpoint != "" {
		endpoint, err := url.Parse(cfg.Endpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return nil, ErrStorage
		}
	}
	options := []func(*config.LoadOptions) error{config.WithLogger(logging.Nop{})}
	if cfg.Region != "" {
		options = append(options, config.WithRegion(cfg.Region))
	}
	if cfg.AccessKey != "" {
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)))
	}
	loaded, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, s3Error(ctx, err)
	}
	if loaded.Region == "" {
		loaded.Region = "us-east-1"
	}
	client := s3.NewFromConfig(loaded, func(options *s3.Options) {
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		options.UsePathStyle = cfg.PathStyle
		options.Logger = logging.Nop{}
		options.ClientLogMode = 0
		// SHA-256 is supplied explicitly on uploads; avoid unsolicited checksum
		// algorithms unsupported by some S3-compatible providers.
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &s3Store{client: client, bucket: cfg.Bucket, tempDir: cfg.TempDir}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if err := validatePut(key, body, size, contentType); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.tempDir, "si-blob-*")
	if err != nil {
		return ErrStorage
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := copyVerified(ctx, file, body, size, key); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ErrStorage
	}
	digest, _ := hex.DecodeString(key)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: file,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest)),
	})
	return s3Error(ctx, err)
}

func (s *s3Store) Stat(ctx context.Context, key string) (Metadata, error) {
	if err := validateKey(key); err != nil {
		return Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Metadata{}, s3Error(ctx, err)
	}
	if output.ContentLength == nil || *output.ContentLength < 0 || *output.ContentLength > MaxSize || validateContentType(aws.ToString(output.ContentType)) != nil {
		return Metadata{}, ErrStorage
	}
	return Metadata{Size: *output.ContentLength, ContentType: aws.ToString(output.ContentType), ETag: strings.Trim(aws.ToString(output.ETag), `"`)}, nil
}

func (s *s3Store) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	metadata, err := s.Stat(ctx, key)
	if err != nil {
		return nil, err
	}
	count, err := rangeLength(metadata.Size, offset, length)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		empty := io.NopCloser(strings.NewReader(""))
		return &objectReader{ctx: ctx, reader: empty, closer: empty}, nil
	}
	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	rangeRequested := offset != 0 || length != -1
	if rangeRequested {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+count-1))
	}
	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, s3Error(ctx, err)
	}
	if output.Body == nil {
		return nil, ErrStorage
	}
	if output.ContentLength == nil || *output.ContentLength != count || (rangeRequested && aws.ToString(output.ContentRange) != fmt.Sprintf("bytes %d-%d/%d", offset, offset+count-1, metadata.Size)) {
		output.Body.Close()
		return nil, ErrStorage
	}
	return &objectReader{ctx: ctx, reader: io.LimitReader(output.Body, count), closer: output.Body}, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	safe := s3Error(ctx, err)
	if errors.Is(safe, ErrNotFound) {
		return nil
	}
	return safe
}

// Never retain or wrap SDK errors: their messages and unwrap chains can contain
// credentials, provider messages, signed URLs, and endpoint details.
func s3Error(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		switch response.HTTPStatusCode() {
		case http.StatusNotFound:
			return ErrNotFound
		case http.StatusRequestedRangeNotSatisfiable:
			return ErrInvalidRange
		}
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return ErrNotFound
		case "InvalidRange":
			return ErrInvalidRange
		}
	}
	return ErrStorage
}
