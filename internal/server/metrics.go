package server

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"siplatform/internal/telemetry"
	"strings"
	"sync/atomic"
	"time"
)

func (s *Server) Metrics() *telemetry.Metrics { return s.metrics }
func (s *Server) liveMetrics() telemetry.Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := telemetry.Live{Connections: len(s.connections), Rooms: len(s.rooms)}
	for _, r := range s.rooms {
		v.Queued += len(r.requests)
	}
	return v
}

type measuredBody struct {
	io.ReadCloser
	bytes atomic.Int64
}

func (b *measuredBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.bytes.Add(int64(n))
	return n, e
}

type measuredResponse struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *measuredResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *measuredResponse) WriteHeader(status int) {
	if w.status == 0 && (status >= 200 || status == 101) {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *measuredResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, e
}
func (w *measuredResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, e := http.NewResponseController(w.ResponseWriter).Hijack()
	if e == nil && w.status == 0 {
		w.status = 101
	}
	return c, b, e
}
func metricCategory(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/account/"):
		return "account"
	case path == "/api/catalogue" || strings.HasPrefix(path, "/api/catalogue/"):
		return "catalogue"
	case path == "/api/mcp":
		return "mcp"
	case strings.HasPrefix(path, "/api/rooms/") && strings.HasSuffix(path, "/ws"):
		return "websocket"
	case strings.HasPrefix(path, "/api/rooms"):
		return "game"
	case strings.HasPrefix(path, "/api/uploads") || strings.HasPrefix(path, "/api/media-uploads"):
		return "media"
	case strings.HasPrefix(path, "/api/drafts") || strings.HasPrefix(path, "/api/themes") || strings.HasPrefix(path, "/api/ai/") || path == "/api/catalogue-imports":
		return "authoring"
	default:
		return "other"
	}
}
func (s *Server) measureHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		out := &measuredResponse{ResponseWriter: w}
		var body *measuredBody
		if r.Body != nil {
			body = &measuredBody{ReadCloser: r.Body}
			r.Body = body
		}
		defer func() {
			var in int64
			if body != nil {
				in = body.bytes.Load()
			}
			status := out.status
			if status == 0 {
				status = 200
			}
			s.metrics.ObserveHTTP(metricCategory(r.URL.Path), r.Method, status, time.Since(start), in, out.bytes)
		}()
		next.ServeHTTP(out, r)
	})
}
