// Package blob provides private, content-addressed streaming object storage.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

var (
	ErrNotFound           = errors.New("blob: object not found")
	ErrInvalidKey         = errors.New("blob: invalid key")
	ErrInvalidRange       = errors.New("blob: invalid range")
	ErrInvalidSize        = errors.New("blob: invalid size")
	ErrHashMismatch       = errors.New("blob: content hash mismatch")
	ErrInvalidContentType = errors.New("blob: invalid content type")
	ErrStorage            = errors.New("blob: storage operation failed")
)

const MaxSize int64 = 250 * 1024 * 1024

// Metadata describes the payload. ETag is an opaque, unquoted validator.
type Metadata struct {
	Size        int64
	ContentType string
	ETag        string
}

// Store uses lowercase SHA-256 hex keys. Delete is idempotent. A length of -1
// reads from offset to EOF; other ranges must fit entirely within the object.
// Put stages and verifies all bytes before publication. Readers must respond to
// context cancellation themselves if their Read method can block indefinitely.
type Store interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Stat(ctx context.Context, key string) (Metadata, error)
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

func validateKey(key string) error {
	if len(key) != 64 {
		return ErrInvalidKey
	}
	for _, c := range key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ErrInvalidKey
		}
	}
	return nil
}

func validatePut(key string, body io.Reader, size int64, contentType string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if size < 0 || size > MaxSize || body == nil {
		return ErrInvalidSize
	}
	return validateContentType(contentType)
}

func validateContentType(contentType string) error {
	if len(contentType) > 4096 || !utf8.ValidString(contentType) || strings.ContainsAny(contentType, "\r\n\x00") {
		return ErrInvalidContentType
	}
	return nil
}

func rangeLength(size, offset, length int64) (int64, error) {
	if offset < 0 || offset > size || length < -1 {
		return 0, ErrInvalidRange
	}
	remaining := size - offset
	if length == -1 {
		return remaining, nil
	}
	if length > remaining {
		return 0, ErrInvalidRange
	}
	return length, nil
}

// copyVerified never reads more than the declared size plus one byte and uses a
// fixed-size buffer. The extra byte detects oversized bodies without buffering.
func copyVerified(ctx context.Context, dst io.Writer, body io.Reader, size int64, key string) error {
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, hash), io.LimitReader(contextReader{ctx: ctx, reader: body}, size+1), make([]byte, 32*1024))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return ErrStorage
	}
	if n != size {
		return ErrInvalidSize
	}
	if hex.EncodeToString(hash.Sum(nil)) != key {
		return ErrHashMismatch
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

type objectReader struct {
	ctx    context.Context
	reader io.Reader
	closer io.Closer
}

func (r *objectReader) Read(p []byte) (int, error) {
	n, err := (contextReader{ctx: r.ctx, reader: r.reader}).Read(p)
	if err != nil && !errors.Is(err, io.EOF) && r.ctx.Err() == nil {
		return n, ErrStorage
	}
	return n, err
}
func (r *objectReader) Close() error {
	if err := r.closer.Close(); err != nil {
		return ErrStorage
	}
	return nil
}
