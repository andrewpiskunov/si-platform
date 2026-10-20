// Package siq reads and writes bounded SIQuester archives without executing content.
package siq

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"siplatform/internal/packs"
)

type Limits struct {
	ArchiveBytes, UnpackedBytes, XMLBytes, MediaBytes int64
	Files, XMLNodes, XMLDepth                         int
}

func DefaultLimits() Limits { return Limits{250 << 20, 1 << 30, 10 << 20, 100 << 20, 5000, 200000, 64} }

type Asset struct {
	Name   string                        `json:"name"`
	Key    string                        `json:"key"`
	Size   int64                         `json:"size"`
	SHA256 string                        `json:"sha256"`
	Open   func() (io.ReadCloser, error) `json:"-"`
}
type Archive struct {
	Document packs.Document `json:"document"`
	Issues   []packs.Issue  `json:"issues"`
	Assets   []Asset        `json:"assets"`
}

func Read(ctx context.Context, input io.ReaderAt, size int64, limits Limits) (*Archive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limits.ArchiveBytes <= 0 || limits.UnpackedBytes <= 0 || limits.XMLBytes <= 0 || limits.MediaBytes <= 0 || limits.Files <= 0 || limits.XMLNodes <= 0 || limits.XMLDepth <= 0 {
		return nil, errors.New("invalid archive limits")
	}
	if size < 22 || size > limits.ArchiveBytes {
		return nil, errors.New("archive size limit exceeded")
	}
	if err := preflight(input, size, limits); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(input, size)
	if err != nil {
		return nil, fmt.Errorf("invalid ZIP: %w", err)
	}
	if len(zr.File) > limits.Files {
		return nil, errors.New("file count limit exceeded")
	}
	seen := map[string]bool{}
	var total uint64
	var content *zip.File
	out := &Archive{}
	for _, f := range zr.File {
		key, err := canonicalName(f.Name)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fmt.Errorf("ambiguous file name: %s", f.Name)
		}
		seen[key] = true
		mode := f.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return nil, errors.New("non-regular ZIP entry")
		}
		if mode.IsDir() {
			continue
		}
		switch strings.ToLower(path.Ext(key)) {
		case ".zip", ".siq", ".7z", ".rar", ".tar", ".gz":
			return nil, errors.New("nested archives are not accepted")
		}
		if f.Flags&1 != 0 {
			return nil, errors.New("encrypted archive is not accepted")
		}
		limit := limits.MediaBytes
		if key == "content.xml" {
			limit = limits.XMLBytes
			content = f
		}
		if f.UncompressedSize64 > uint64(limit) || total > uint64(limits.UnpackedBytes)-min(f.UncompressedSize64, uint64(limits.UnpackedBytes)) {
			return nil, errors.New("unpacked file size limit exceeded")
		}
		total += f.UncompressedSize64
		if total > uint64(limits.UnpackedBytes) {
			return nil, errors.New("unpacked total limit exceeded")
		}
	}
	if content == nil {
		return nil, errors.New("content.xml is missing")
	}
	// Stream every entry to EOF: this checks actual size and ZIP CRC, including unreferenced media.
	var xmlData []byte
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, _ := canonicalName(f.Name)
		limit := limits.MediaBytes
		if f == content {
			limit = limits.XMLBytes
		}
		stream, err := f.Open()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		var xmlBuf bytes.Buffer
		writer := io.Writer(hash)
		if f == content {
			writer = io.MultiWriter(hash, &xmlBuf)
		}
		n, copyErr := io.Copy(writer, io.LimitReader(contextReader{ctx, stream}, limit+1))
		closeErr := stream.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("corrupt file %s: %w", f.Name, copyErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n > limit || uint64(n) != f.UncompressedSize64 {
			return nil, errors.New("actual file size differs from archive declaration")
		}
		if f == content {
			xmlData = xmlBuf.Bytes()
		} else {
			out.Assets = append(out.Assets, Asset{Name: f.Name, Key: key, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), Open: f.Open})
		}
	}
	doc, err := parseXML(ctx, xmlData, limits)
	if err != nil {
		return nil, err
	}
	out.Document = doc
	out.Issues = append(packs.Validate(doc), mediaIssues(doc, out.Assets)...)
	return out, nil
}
func canonicalName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errors.New("file name is not UTF-8")
	}
	decoded := name
	for i := 0; i < 4 && strings.Contains(decoded, "%"); i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", errors.New("invalid encoded file name")
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	if decoded == "" || strings.ContainsAny(decoded, "\\:%") || strings.HasPrefix(decoded, "/") {
		return "", errors.New("unsafe file path")
	}
	for _, r := range decoded {
		if unicode.IsControl(r) {
			return "", errors.New("control character in file name")
		}
	}
	decoded = strings.TrimSuffix(decoded, "/")
	for _, part := range strings.Split(decoded, "/") {
		if part == ".." || part == "." || part == "" {
			return "", errors.New("unsafe file path segment")
		}
	}
	return strings.ToLower(norm.NFC.String(decoded)), nil
}
func preflight(input io.ReaderAt, size int64, limits Limits) error {
	tail := make([]byte, min(size, 65557))
	if _, err := input.ReadAt(tail, size-int64(len(tail))); err != nil {
		return err
	}
	at := -1
	for pos := len(tail) - 22; pos >= 0; pos-- {
		if bytes.Equal(tail[pos:pos+4], []byte("PK\x05\x06")) && pos+22+int(binary.LittleEndian.Uint16(tail[pos+20:])) == len(tail) {
			at = pos
			break
		}
	}
	if at < 0 {
		return errors.New("ZIP end record is missing")
	}
	h := tail[at:]
	disk := binary.LittleEndian.Uint16(h[4:])
	centralDisk := binary.LittleEndian.Uint16(h[6:])
	onDisk := binary.LittleEndian.Uint16(h[8:])
	count := binary.LittleEndian.Uint16(h[10:])
	directoryBytes := binary.LittleEndian.Uint32(h[12:])
	offset := binary.LittleEndian.Uint32(h[16:])
	if disk != 0 || centralDisk != 0 || onDisk != count || count == 65535 || directoryBytes == 0xffffffff || offset == 0xffffffff {
		return errors.New("ZIP64 or multi-volume ZIP is outside the supported profile")
	}
	if int(count) > limits.Files || directoryBytes > 8<<20 {
		return errors.New("ZIP directory limit exceeded")
	}
	if uint64(offset)+uint64(directoryBytes) > uint64(size-int64(len(tail))+int64(at)) {
		return errors.New("invalid ZIP directory bounds")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}
