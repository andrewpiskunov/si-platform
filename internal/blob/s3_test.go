package blob_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"siplatform/internal/blob"
)

type wireObject struct{ data, contentType string }
type s3Wire struct {
	mu            sync.Mutex
	objects       map[string]wireObject
	requests      []string
	ranges        []string
	authorization string
	token         string
	problem       string
}

func (w *s3Wire) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, r.Method+" "+r.URL.Path)
	w.authorization = r.Header.Get("Authorization")
	w.token = r.Header.Get("X-Amz-Security-Token")
	if r.Method == "GET" && r.URL.Query().Has("versions") {
		rw.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(rw, "<ListVersionsResult><IsTruncated>false</IsTruncated>")
		key := r.URL.Query().Get("prefix")
		if _, ok := w.objects[key]; ok {
			fmt.Fprintf(rw, "<Version><Key>%s</Key><VersionId>wire-version</VersionId></Version>", key)
		}
		fmt.Fprint(rw, "</ListVersionsResult>")
		return
	}

	if r.URL.RawQuery != "" && r.URL.RawQuery != "x-id=PutObject" && r.URL.RawQuery != "x-id=GetObject" && r.URL.RawQuery != "x-id=DeleteObject" && r.URL.Query().Get("versionId") != "wire-version" {
		w.problem = "unexpected query: " + r.URL.RawQuery
	}
	if !strings.HasPrefix(r.URL.Path, "/private-test/") || r.Header.Get("X-Amz-Acl") != "" {
		w.problem = "wrong object path or ACL mutation"
	}
	key := strings.TrimPrefix(r.URL.Path, "/private-test/")
	switch r.Method {
	case "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.problem = "upload read failed"
			rw.WriteHeader(400)
			return
		}
		if int64(len(data)) != r.ContentLength {
			w.problem = "wrong Content-Length"
		}
		w.objects[key] = wireObject{string(data), r.Header.Get("Content-Type")}
		rw.Header().Set("ETag", `"remote-etag"`)
		rw.WriteHeader(200)
	case "HEAD", "GET":
		object, ok := w.objects[key]
		if !ok {
			rw.WriteHeader(404)
			return
		}
		data := object.data
		rw.Header().Set("Content-Type", object.contentType)
		rw.Header().Set("ETag", `"remote-etag"`)
		status := 200
		if r.Method == "GET" && r.Header.Get("Range") != "" {
			span := r.Header.Get("Range")
			w.ranges = append(w.ranges, span)
			var start, end int
			if _, err := fmt.Sscanf(span, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= len(data) {
				rw.WriteHeader(416)
				return
			}
			rw.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			data = data[start : end+1]
			status = 206
		}
		rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
		rw.WriteHeader(status)
		if r.Method == "GET" {
			_, _ = io.WriteString(rw, data)
		}
	case "DELETE":
		if r.URL.Query().Get("versionId") != "wire-version" {
			w.problem = "unsafe unversioned deletion"
			rw.WriteHeader(400)
			return
		}
		delete(w.objects, key)
		rw.WriteHeader(204)
	default:
		w.problem = "unexpected method: " + r.Method
		rw.WriteHeader(405)
	}
}

func newS3(t *testing.T, handler http.Handler) (blob.Store, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	staging := t.TempDir()
	s, err := blob.NewS3(context.Background(), blob.S3Config{Endpoint: srv.URL, Region: "test-region-1", Bucket: "private-test", AccessKey: "test-access", SecretKey: "test-secret", SessionToken: "test-session", PathStyle: true, TempDir: staging})
	if err != nil {
		t.Fatal(err)
	}
	return s, staging
}

func TestS3SDKRequestsAndRanges(t *testing.T) {
	wire := &s3Wire{objects: make(map[string]wireObject)}
	s, staging := newS3(t, wire)
	key := keyFor("hello world")
	ctx := context.Background()
	if err := s.Put(ctx, key, strings.NewReader("hello world"), 11, "text/plain"); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.Stat(ctx, key)
	if err != nil || metadata.Size != 11 || metadata.ContentType != "text/plain" || metadata.ETag != "remote-etag" {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	if got := readObject(t, s, key, 0, -1); got != "hello world" {
		t.Fatalf("whole = %q", got)
	}
	if got := readObject(t, s, key, 6, 5); got != "world" {
		t.Fatalf("range = %q", got)
	}
	if got := readObject(t, s, key, 6, -1); got != "world" {
		t.Fatalf("tail = %q", got)
	}
	if got := readObject(t, s, key, 11, 0); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Stat missing = %v", err)
	}
	if _, err := s.Get(ctx, key, 0, -1); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("repeat Delete = %v", err)
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if wire.problem != "" {
		t.Fatal(wire.problem)
	}
	if !strings.Contains(wire.authorization, "Credential=test-access/") || wire.token != "test-session" {
		t.Fatalf("configured credentials were not used")
	}
	if strings.Join(wire.ranges, ",") != "bytes=6-10,bytes=6-10" {
		t.Fatalf("Range headers = %v", wire.ranges)
	}
	files, err := os.ReadDir(staging)
	if err != nil || len(files) != 0 {
		t.Fatalf("staging remains = %v %v", files, err)
	}
}

func TestS3InvalidUploadsNeverReachProvider(t *testing.T) {
	wire := &s3Wire{objects: make(map[string]wireObject)}
	s, staging := newS3(t, wire)
	ctx := context.Background()
	key := keyFor("abc")
	if err := s.Put(ctx, key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		size int64
	}{{"abc", 2}, {"abc", 4}, {"xyz", 3}, {"", -1}, {"", 250*1024*1024 + 1}} {
		if err := s.Put(ctx, key, strings.NewReader(tc.body), tc.size, "application/json"); err == nil {
			t.Fatal("invalid upload accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	if err := s.Put(canceled, key, &cancelReader{cancel: cancel}, 3, "text/plain"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled staging = %v", err)
	}
	if err := s.Put(canceled, key, strings.NewReader("abc"), 3, "text/plain"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Put = %v", err)
	}
	if _, err := s.Stat(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Stat = %v", err)
	}
	if _, err := s.Get(canceled, key, 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get = %v", err)
	}
	if err := s.Delete(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Delete = %v", err)
	}
	if err := s.Put(ctx, "../escape", strings.NewReader("abc"), 3, "text/plain"); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("key = %v", err)
	}
	if _, err := s.Stat(ctx, "bad"); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Stat key = %v", err)
	}
	if _, err := s.Get(ctx, "bad", 0, -1); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Get key = %v", err)
	}
	if err := s.Delete(ctx, "bad"); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("Delete key = %v", err)
	}
	wire.mu.Lock()
	if len(wire.requests) != 1 {
		t.Errorf("invalid uploads reached provider: %v", wire.requests)
	}
	wire.mu.Unlock()
	if got := readObject(t, s, key, 0, -1); got != "abc" {
		t.Fatalf("existing object damaged: %q", got)
	}
	for _, tc := range []struct{ offset, length int64 }{{-1, 1}, {0, -2}, {4, -1}, {2, 2}, {1, 1<<63 - 1}} {
		if r, err := s.Get(ctx, key, tc.offset, tc.length); !errors.Is(err, blob.ErrInvalidRange) {
			if r != nil {
				r.Close()
			}
			t.Errorf("invalid range = %v", err)
		}
	}
	files, err := os.ReadDir(staging)
	if err != nil || len(files) != 0 {
		t.Fatalf("staging remains = %v %v", files, err)
	}
}

func TestS3ProviderErrorsAreSanitized(t *testing.T) {
	const secret = "leaked-secret-provider-details"
	s, staging := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(403)
		fmt.Fprintf(w, "<Error><Code>AccessDenied</Code><Message>%s %s %s</Message></Error>", secret, r.Host, r.Header.Get("Authorization"))
	}))
	key := keyFor("abc")
	operations := []func() error{
		func() error { return s.Put(context.Background(), key, strings.NewReader("abc"), 3, "text/plain") },
		func() error { _, err := s.Stat(context.Background(), key); return err },
		func() error { _, err := s.Get(context.Background(), key, 0, -1); return err },
		func() error { return s.Delete(context.Background(), key) },
	}
	for _, op := range operations {
		err := op()
		if err == nil {
			t.Fatal("provider error lost")
		}
		for current := err; current != nil; current = errors.Unwrap(current) {
			for _, value := range []string{secret, "test-access", "test-secret", "test-session", "127.0.0.1", "Credential="} {
				if strings.Contains(fmt.Sprintf("%+v", current), value) {
					t.Fatalf("error disclosed provider details: %v", current)
				}
			}
		}
	}
	files, err := os.ReadDir(staging)
	if err != nil || len(files) != 0 {
		t.Fatalf("failed request staging remains: %v %v", files, err)
	}
}

func TestS3UsesDefaultEnvironmentCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-session")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	wire := &s3Wire{objects: map[string]wireObject{keyFor("abc"): {"abc", "text/plain"}}}
	srv := httptest.NewServer(wire)
	defer srv.Close()
	s, err := blob.NewS3(context.Background(), blob.S3Config{Endpoint: srv.URL, Region: "test-region-1", Bucket: "private-test", PathStyle: true, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(context.Background(), keyFor("abc")); err != nil {
		t.Fatal(err)
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if !strings.Contains(wire.authorization, "Credential=environment-access/") || wire.token != "environment-session" {
		t.Fatal("default credential chain was not used")
	}
}

func TestS3RejectsInvalidConfigurationWithoutDisclosingIt(t *testing.T) {
	for _, cfg := range []blob.S3Config{
		{Bucket: "private-test", Endpoint: "http://user:secret@localhost"},
		{Bucket: "private-test", Endpoint: "http://localhost/?token=secret"},
		{Bucket: "private-test", Endpoint: "http://%secret"},
		{Bucket: "private-test", AccessKey: "secret"},
		{Endpoint: "http://localhost"},
	} {
		if _, err := blob.NewS3(context.Background(), cfg); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe configuration error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := blob.NewS3(ctx, blob.S3Config{Bucket: "private-test"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor cancellation = %v", err)
	}
}

func TestS3RejectsIgnoredRangeResponse(t *testing.T) {
	s, _ := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "3")
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(200)
		if r.Method == "GET" {
			_, _ = io.WriteString(w, "abc")
		}
	}))
	if r, err := s.Get(context.Background(), keyFor("abc"), 1, 1); err == nil {
		r.Close()
		t.Fatal("ignored range accepted")
	}
}

func TestS3NetworkCancellationRemovesStaging(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, staging := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}))
	err := s.Put(ctx, keyFor("abc"), strings.NewReader("abc"), 3, "text/plain")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("network cancellation = %v", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled staging = %v %v", entries, err)
	}
}

func TestS3MapsRangeFailureAndSanitizesStreamingFailure(t *testing.T) {
	for _, status := range []int{416, 200} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s, _ := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"etag"`)
				if r.Method == "HEAD" {
					w.Header().Set("Content-Length", "3")
					return
				}
				if status == 416 {
					w.WriteHeader(416)
					return
				}
				w.Header().Set("Content-Length", "3")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "a")
			}))
			r, err := s.Get(context.Background(), keyFor("abc"), 0, -1)
			if status == 416 {
				if r != nil {
					r.Close()
				}
				if !errors.Is(err, blob.ErrInvalidRange) {
					t.Fatalf("range error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := io.ReadAll(r); !errors.Is(err, blob.ErrStorage) {
				t.Fatalf("truncated stream error = %v", err)
			}
		})
	}
}
