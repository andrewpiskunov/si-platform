//go:build unix

package catalogueimport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSpecialFilesAndCancelledPreflightReturnPromptly(t *testing.T) {
	for _, mode := range []string{"manifest_fifo", "item_fifo", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			name, m := pilot(t)
			var unblock string
			if mode == "manifest_fifo" {
				os.Remove(name)
				if e := syscall.Mkfifo(name, 0600); e != nil {
					t.Fatal(e)
				}
				unblock = name
			}
			if mode == "item_fifo" {
				file := filepath.Join(filepath.Dir(name), "pipe.siq")
				if e := syscall.Mkfifo(file, 0600); e != nil {
					t.Fatal(e)
				}
				m.Items[0].File = "pipe.siq"
				raw, _ := json.Marshal(m)
				os.WriteFile(name, raw, 0600)
				unblock = file
			}
			var calls atomic.Int32
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer h.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, e := Run(ctx, name, h.URL, "secret"); done <- e }()
			select {
			case e := <-done:
				if e == nil {
					t.Error("accepted", mode)
				}
			case <-time.After(300 * time.Millisecond):
				// Unblock the old implementation so the RED test leaves no goroutine behind.
				cancel()
				if unblock != "" {
					f, e := os.OpenFile(unblock, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
					if e == nil {
						f.Close()
					}
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Error("preflight ignored cancellation or blocked opening special file")
			}
			if calls.Load() != 0 {
				t.Error("preflight mutated server", calls.Load())
			}
		})
	}
}

func TestCancelledBatchReportsEveryManifestEntry(t *testing.T) {
	name, m := pilot(t)
	second := m.Items[0]
	second.ExternalID = "2"
	m.Items = append(m.Items, second)
	raw, _ := json.Marshal(m)
	os.WriteFile(name, raw, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":17,"offset":17,"status":"ready","draft_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
		cancel()
	}))
	defer h.Close()
	report, e := Run(ctx, name, h.URL, "secret")
	if e == nil || len(report) != 2 || report[1].Status != "not_attempted" {
		t.Fatal(report, e)
	}
}

type cancelOnRead struct {
	cancel context.CancelFunc
	calls  int
}

func (r *cancelOnRead) Read(p []byte) (int, error) { r.calls++; r.cancel(); return len(p), nil }
func TestHashReaderChecksCancellationBetweenBoundedReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelOnRead{cancel: cancel}
	reader := contextReader{ctx, source}
	buf := make([]byte, 1<<20)
	n, e := reader.Read(buf)
	if e != nil || n != 32<<10 {
		t.Fatal(n, e)
	}
	n, e = reader.Read(buf)
	if e != context.Canceled || n != 0 || source.calls != 1 {
		t.Fatal(n, e, source.calls)
	}
}
