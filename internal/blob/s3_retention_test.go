package blob_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"siplatform/internal/blob"
)

func TestS3DeleteRejectsMutableVersions(t *testing.T) {
	for _, version := range []string{"", "null"} {
		t.Run("version="+version, func(t *testing.T) {
			var deletes atomic.Int32
			s, _ := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes.Add(1)
					w.WriteHeader(204)
					return
				}
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>%s</VersionId></Version></ListVersionsResult>", keyFor("abc"), version)
			}))
			if err := s.Delete(context.Background(), keyFor("abc")); !errors.Is(err, blob.ErrStorage) {
				t.Fatalf("unsafe version accepted: %v", err)
			}
			if deletes.Load() != 0 {
				t.Fatal("mutable key deleted")
			}
		})
	}
}

func TestS3DeleteProcessesBoundedVersionsAndMarkers(t *testing.T) {
	var pass atomic.Int32
	var deleted []string
	s, _ := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = append(deleted, r.URL.Query().Get("versionId"))
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("max-keys") != "100" {
			t.Error("unbounded listing")
		}
		w.Header().Set("Content-Type", "application/xml")
		if pass.Add(1) == 1 {
			fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>true</IsTruncated><Version><Key>%s</Key><VersionId>v1</VersionId></Version><DeleteMarker><Key>%s</Key><VersionId>marker1</VersionId></DeleteMarker><Version><Key>unrelated</Key><VersionId>foreign</VersionId></Version></ListVersionsResult>", keyFor("abc"), keyFor("abc"))
		} else {
			fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>v2</VersionId></Version></ListVersionsResult>", keyFor("abc"))
		}
	}))
	if err := s.Delete(context.Background(), keyFor("abc")); !errors.Is(err, blob.ErrStorage) {
		t.Fatalf("truncated cleanup reported complete: %v", err)
	}
	if err := s.Delete(context.Background(), keyFor("abc")); err != nil {
		t.Fatal(err)
	}
	if strings.Join(deleted, ",") != "v1,marker1,v2" {
		t.Fatalf("wrong versions: %v", deleted)
	}
}

func TestS3RequiredVersioningAndSuspendedUpload(t *testing.T) {
	for _, status := range []string{"Enabled", "Suspended", ""} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("versioning") {
					w.Header().Set("Content-Type", "application/xml")
					fmt.Fprintf(w, "<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>", status)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				// Simulates versioning being suspended after the startup check.
				w.Header().Set("x-amz-version-id", "null")
				w.WriteHeader(200)
			}))
			defer srv.Close()
			s, err := blob.NewS3(context.Background(), blob.S3Config{Endpoint: srv.URL, Region: "test", Bucket: "private-test", AccessKey: "key", SecretKey: "secret", PathStyle: true, RequireVersioning: true, TempDir: t.TempDir()})
			if status != "Enabled" {
				if !errors.Is(err, blob.ErrStorage) {
					t.Fatalf("unsafe bucket accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Put(context.Background(), keyFor("abc"), strings.NewReader("abc"), 3, "text/plain"); !errors.Is(err, blob.ErrStorage) {
				t.Fatalf("null version publication reported success: %v", err)
			}
		})
	}
}

func TestS3DeletionRechecksGuardAfterRemoteSnapshot(t *testing.T) {
	var listed, deleted atomic.Bool
	s, _ := newS3(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted.Store(true)
			w.WriteHeader(204)
			return
		}
		listed.Store(true)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>v1</VersionId></Version></ListVersionsResult>", keyFor("abc"))
	}))
	lost := errors.New("guard lost during version snapshot")
	err := blob.DeleteGuarded(context.Background(), s, keyFor("abc"), func(context.Context) error {
		if !listed.Load() {
			t.Fatal("guard checked before snapshot")
		}
		return lost
	})
	if !errors.Is(err, lost) || deleted.Load() {
		t.Fatalf("delete after guard loss: %v %v", err, deleted.Load())
	}
}
