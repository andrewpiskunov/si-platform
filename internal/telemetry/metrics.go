// Package telemetry owns bounded operational metrics; labels never contain user data.
package telemetry

import (
	"crypto/subtle"
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"strings"
	"sync"
	"time"
)

var buckets = []float64{.001, .005, .01, .03, .075, .2, .3, 1, 3, 5, 15}

type Live struct{ Connections, Rooms, Queued int }
type Metrics struct {
	database                     *databaseMetrics
	Registry                     *prometheus.Registry
	requests                     *prometheus.CounterVec
	httpDuration                 *prometheus.HistogramVec
	received, sent, wsSent, slow prometheus.Counter
	queue, processing            *prometheus.HistogramVec
	liveMu                       sync.RWMutex
	live                         func() Live
}

func New() *Metrics {
	m := &Metrics{Registry: prometheus.NewRegistry()}
	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "si_http_requests_total", Help: "Completed HTTP requests, including websocket lifetime completion."}, []string{"category", "method", "status"})
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "si_http_duration_seconds", Help: "HTTP handler duration excluding WebSocket lifetimes.", Buckets: buckets}, []string{"category", "method", "status"})
	counter := func(name, help string) prometheus.Counter {
		return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	}
	m.received = counter("si_http_received_bytes_total", "HTTP request body bytes actually read; excludes WebSocket, TLS and headers.")
	m.sent = counter("si_http_sent_bytes_total", "HTTP response body bytes written; excludes WebSocket, TLS and headers.")
	m.wsSent = counter("si_ws_sent_bytes_total", "Successfully written WebSocket application payload bytes; excludes framing and TLS.")
	m.slow = counter("si_ws_slow_clients_total", "WebSocket clients disconnected because their outgoing queue was full.")
	m.queue = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "si_room_queue_seconds", Help: "Time waiting for a room owner to start a job.", Buckets: buckets}, []string{"kind", "outcome"})
	m.processing = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "si_room_processing_seconds", Help: "Room job duration including persistence and outbound enqueue, excluding network and queue wait.", Buckets: buckets}, []string{"kind", "outcome"})
	m.Registry.MustRegister(m.requests, m.httpDuration, m.received, m.sent, m.wsSent, m.slow, m.queue, m.processing, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	for _, v := range []struct {
		name, help string
		value      func(Live) int
	}{{"si_ws_connections", "Currently admitted WebSocket connections.", func(s Live) int { return s.Connections }}, {"si_rooms_loaded", "Room owners currently loaded in this process.", func(s Live) int { return s.Rooms }}, {"si_room_jobs_queued", "Jobs currently queued across loaded room owners.", func(s Live) int { return s.Queued }}} {
		m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: v.name, Help: v.help}, func() float64 {
			m.liveMu.RLock()
			fn := m.live
			m.liveMu.RUnlock()
			if fn == nil {
				return 0
			}
			return float64(v.value(fn()))
		}))
	}
	m.initDatabase()
	return m
}
func (m *Metrics) SetLive(fn func() Live) { m.liveMu.Lock(); m.live = fn; m.liveMu.Unlock() }
func (m *Metrics) ObserveHTTP(category, method string, status int, d time.Duration, in, out int64) {
	switch category {
	case "account", "catalogue", "authoring", "game", "media", "mcp", "websocket":
	default:
		category = "other"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		method = "OTHER"
	}
	class := "other"
	switch status / 100 {
	case 1:
		class = "1xx"
	case 2:
		class = "2xx"
	case 3:
		class = "3xx"
	case 4:
		class = "4xx"
	case 5:
		class = "5xx"
	}
	m.requests.WithLabelValues(category, method, class).Inc()
	if category != "websocket" {
		m.httpDuration.WithLabelValues(category, method, class).Observe(d.Seconds())
	}
	if in > 0 {
		m.received.Add(float64(in))
	}
	if out > 0 {
		m.sent.Add(float64(out))
	}
}
func (m *Metrics) ObserveRoom(kind string, queue, processing time.Duration, failed bool) {
	if kind != "command" {
		kind = "work"
	}
	outcome := "ok"
	if failed {
		outcome = "error"
	}
	m.queue.WithLabelValues(kind, outcome).Observe(max(0, queue.Seconds()))
	m.processing.WithLabelValues(kind, outcome).Observe(max(0, processing.Seconds()))
}
func (m *Metrics) WSBytes(n int) {
	if n > 0 {
		m.wsSent.Add(float64(n))
	}
}
func (m *Metrics) SlowClient() { m.slow.Inc() }
func (m *Metrics) Handler(token string) (http.Handler, error) {
	if e := ValidateToken(token); e != nil {
		return nil, e
	}
	handler := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 5 * time.Second})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.RawQuery != "" {
			http.Error(w, "invalid", 400)
			return
		}
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", 405)
			return
		}
		auth := r.Header.Values("Authorization")
		valid := false
		if len(auth) == 1 {
			parts := strings.Fields(auth[0])
			valid = len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", `Bearer realm="si-platform-metrics"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		handler.ServeHTTP(w, r)
	}), nil
}

func ValidateToken(token string) error {
	if len(token) < 32 || len(token) > 256 {
		return errors.New("invalid metrics token")
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return errors.New("invalid metrics token")
		}
	}
	return nil
}
