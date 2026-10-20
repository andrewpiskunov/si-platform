package blob_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"siplatform/internal/blob"
)

func keyFor(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func newLocal(t *testing.T) (blob.Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func readObject(t *testing.T, s blob.Store, key string, offset, length int64) string {
	t.Helper()
	r, err := s.Get(context.Background(), key, offset, length)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLocalPersistsBytesAndMetadataAcrossReopen(t *testing.T) {
	s, root := newLocal(t)
	ctx := context.Background()
	data := "hello world"
	key := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if err := s.Put(ctx, key, strings.NewReader(data), 11, "text/plain"); err != nil {
		t.Fatal(err)
	}
	reopened, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := reopened.Stat(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != 11 || meta.ContentType != "text/plain" || meta.ETag != key {
		t.Fatalf("metadata = %+v", meta)
	}
	if got := readObject(t, reopened, key, 0, -1); got != data {
		t.Fatalf("data = %q", got)
	}
	if err := reopened.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Stat missing = %v", err)
	}
	if _, err := s.Get(ctx, key, 0, -1); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("idempotent delete = %v", err)
	}
}

func TestLocalRanges(t *testing.T) {
	s, _ := newLocal(t)
	key := keyFor("abcdefghij")
	if err := s.Put(context.Background(), key, strings.NewReader("abcdefghij"), 10, "text/plain"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset, length int64
		want           string
	}{{0, -1, "abcdefghij"}, {2, 3, "cde"}, {3, -1, "defghij"}, {9, 1, "j"}, {10, 0, ""}, {10, -1, ""}, {0, 0, ""}} {
		if got := readObject(t, s, key, tc.offset, tc.length); got != tc.want {
			t.Errorf("Get(%d,%d) = %q, want %q", tc.offset, tc.length, got, tc.want)
		}
	}
	for _, tc := range []struct{ offset, length int64 }{{-1, 1}, {0, -2}, {11, -1}, {9, 2}, {10, 1}, {math.MaxInt64, 2}, {1, math.MaxInt64}} {
		if r, err := s.Get(context.Background(), key, tc.offset, tc.length); !errors.Is(err, blob.ErrInvalidRange) {
			if r != nil {
				r.Close()
			}
			t.Errorf("Get(%d,%d) error = %v", tc.offset, tc.length, err)
		}
	}
}

func TestLocalRejectsInvalidKeysForEveryOperation(t *testing.T) {
	s, _ := newLocal(t)
	for _, key := range []string{"", "../escape", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64), "/" + strings.Repeat("a", 64)} {
		ctx := context.Background()
		if err := s.Put(ctx, key, strings.NewReader(""), 0, "text/plain"); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Put key %q: %v", key, err)
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Stat key %q: %v", key, err)
		}
		if r, err := s.Get(ctx, key, 0, -1); !errors.Is(err, blob.ErrInvalidKey) {
			if r != nil {
				r.Close()
			}
			t.Errorf("Get key %q: %v", key, err)
		}
		if err := s.Delete(ctx, key); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Delete key %q: %v", key, err)
		}
	}
}

func TestLocalFailedPutNeverDamagesExistingObject(t *testing.T) {
	s, root := newLocal(t)
	ctx := context.Background()
	data := "original"
	key := keyFor(data)
	if err := s.Put(ctx, key, strings.NewReader(data), 8, "text/plain"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		size       int64
	}{{"short", "original", 9}, {"long", "original", 7}, {"hash", "imposter", 8}, {"negative", "", -1}, {"over-limit", "", 250*1024*1024 + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Put(ctx, key, strings.NewReader(tc.body), tc.size, "application/json"); err == nil {
				t.Fatal("invalid upload accepted")
			}
			if got := readObject(t, s, key, 0, -1); got != data {
				t.Fatalf("object damaged: %q", got)
			}
			meta, err := s.Stat(ctx, key)
			if err != nil || meta.ContentType != "text/plain" {
				t.Fatalf("metadata damaged: %+v, %v", meta, err)
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 1 {
				t.Fatalf("temporary files remain: %v, %v", files, err)
			}
		})
	}
	if err := s.Put(ctx, key, strings.NewReader(data), 8, "text/plain"); err != nil {
		t.Fatalf("repeat = %v", err)
	}
}

type cancelReader struct {
	cancel context.CancelFunc
	sent   bool
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	copy(p, "abc")
	r.cancel()
	return 3, nil
}

func TestLocalCancellationCleansStagingAndPreservesObject(t *testing.T) {
	s, root := newLocal(t)
	key := keyFor("abc")
	ctx, cancel := context.WithCancel(context.Background())
	err := s.Put(ctx, key, &cancelReader{cancel: cancel}, 3, "text/plain")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put = %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatalf("partial files remain: %v %v", files, err)
	}
	if err := s.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, key, strings.NewReader("abc"), 3, "application/json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled repeat = %v", err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stat = %v", err)
	}
	if _, err := s.Get(ctx, key, 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v", err)
	}
	if err := s.Delete(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete = %v", err)
	}
	readCtx, readCancel := context.WithCancel(context.Background())
	r, err := s.Get(readCtx, key, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	readCancel()
	if _, err := r.Read(make([]byte, 3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read = %v", err)
	}
	if got := readObject(t, s, key, 0, -1); got != "abc" {
		t.Fatalf("cancellation damaged object: %q", got)
	}
}

func TestLocalRejectsSymlinkEscape(t *testing.T) {
	s, root := newLocal(t)
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	key := keyFor("abc")
	link := filepath.Join(root, key)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(context.Background(), key); err == nil {
		t.Fatal("Stat followed symlink")
	}
	if r, err := s.Get(context.Background(), key, 0, -1); err == nil {
		r.Close()
		t.Fatal("Get followed symlink")
	}
	_ = s.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain")
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("escaped root: %q %v", got, err)
	}
}

func TestLocalConcurrentPutsKeepOneCompleteObject(t *testing.T) {
	s, root := newLocal(t)
	data := strings.Repeat("blob", 10000)
	key := keyFor(data)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			second, err := blob.NewLocal(root)
			if err != nil {
				t.Error(err)
				return
			}
			if err := second.Put(context.Background(), key, strings.NewReader(data), int64(len(data)), "text/plain"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := readObject(t, s, key, 123, 7); got != "bblobbl" {
		t.Fatalf("range = %q", got)
	}
	meta, err := s.Stat(context.Background(), key)
	if err != nil || meta.Size != int64(len(data)) || meta.ContentType != "text/plain" {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files remain: %v %v", entries, err)
	}
}

func TestLocalEmptyObject(t *testing.T) {
	s, _ := newLocal(t)
	key := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if err := s.Put(context.Background(), key, bytes.NewReader(nil), 0, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if got := readObject(t, s, key, 0, -1); got != "" {
		t.Fatalf("empty object = %q", got)
	}
}

type countingReader struct{ reads int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.reads += len(p)
	return len(p), nil
}

func TestLocalOversizeBodyReadsOnlyDeclaredLimitPlusOne(t *testing.T) {
	s, root := newLocal(t)
	body := &countingReader{}
	if err := s.Put(context.Background(), keyFor("aaa"), body, 3, "text/plain"); !errors.Is(err, blob.ErrInvalidSize) {
		t.Fatalf("size mismatch = %v", err)
	}
	if body.reads != 4 {
		t.Fatalf("unbounded input consumed: %d", body.reads)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial staging = %v %v", entries, err)
	}
	body = &countingReader{}
	if err := s.Put(context.Background(), keyFor("aaa"), body, 250*1024*1024+1, "text/plain"); !errors.Is(err, blob.ErrInvalidSize) {
		t.Fatalf("over-limit size = %v", err)
	}
	if body.reads != 0 {
		t.Fatal("read an upload whose declared size exceeds the cap")
	}
}

func TestLocalContentTypeSurvivesEscaping(t *testing.T) {
	s, _ := newLocal(t)
	// Less-than signs expand when encoded in JSON; metadata bounds must account
	// for the encoded size and must never accept an unreadable object.
	contentType := strings.Repeat("<", 4096)
	err := s.Put(context.Background(), keyFor("abc"), strings.NewReader("abc"), 3, contentType)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := s.Stat(context.Background(), keyFor("abc"))
	if err != nil || metadata.ContentType != contentType {
		t.Fatalf("published unreadable metadata: %+v, %v", metadata, err)
	}
}

func TestLocalRejectsHeaderInjectionAndNilBody(t *testing.T) {
	s, _ := newLocal(t)
	for _, contentType := range []string{"text/plain\r\nX-Injected: yes", "text/plain\x00", "text/plain\xff", strings.Repeat("a", 4097)} {
		if err := s.Put(context.Background(), keyFor("abc"), strings.NewReader("abc"), 3, contentType); !errors.Is(err, blob.ErrInvalidContentType) {
			t.Fatalf("invalid content type = %v", err)
		}
	}
	if err := s.Put(context.Background(), keyFor("abc"), nil, 3, "text/plain"); !errors.Is(err, blob.ErrInvalidSize) {
		t.Fatalf("nil body = %v", err)
	}
}

func TestLocalCorruptFileReturnsError(t *testing.T) {
	s, root := newLocal(t)
	key := keyFor("abc")
	if err := os.WriteFile(filepath.Join(root, key), []byte("invalid container"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(context.Background(), key); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
	if r, err := s.Get(context.Background(), key, 0, -1); err == nil {
		r.Close()
		t.Fatal("corrupt data exposed")
	}
}

func TestLocalReadsRemainConsistentDuringPuts(t *testing.T) {
	s, root := newLocal(t)
	other, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	key := keyFor("abc")
	if err := s.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := other.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		r, err := s.Get(context.Background(), key, 0, -1)
		if err != nil {
			t.Error(err)
			break
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(data) != "abc" {
			t.Errorf("concurrent read = %q, %v", data, err)
			break
		}
	}
	wg.Wait()
}
