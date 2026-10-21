package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	h, e := m.Handler(strings.Repeat("x", 32))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://metrics/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Body.String()
}
func TestMetricsConcurrentBoundedAndNoPrivateLabels(t *testing.T) {
	m := New()
	m.SetLive(func() Live { return Live{Connections: 3, Rooms: 2, Queued: 1} })
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				m.ObserveHTTP("private-secret-room-id", "evil-user-method", 799, time.Millisecond, 4, 8)
				m.ObserveRoom("private-secret-command", time.Millisecond, 2*time.Millisecond, false)
			}
		}()
	}
	wg.Wait()
	text := scrape(t, m)
	for _, want := range []string{`si_http_requests_total{category="other",method="OTHER",status="other"} 2000`, `si_http_received_bytes_total 8000`, `si_http_sent_bytes_total 16000`, `si_ws_connections 3`, `si_rooms_loaded 2`, `si_room_jobs_queued 1`, `si_room_processing_seconds_count{kind="work",outcome="ok"} 2000`, `process_resident_memory_bytes`, `go_goroutines`} {
		if !strings.Contains(text, want) {
			t.Error("missing", want)
		}
	}
	if strings.Contains(text, "private-secret") || strings.Contains(text, "evil-user") {
		t.Fatal("unbounded/private labels")
	}
}
func TestMetricsEndpointRequiresSeparateTokenAndNoQuery(t *testing.T) {
	m := New()
	if _, e := m.Handler("short"); e == nil {
		t.Fatal("short token accepted")
	}
	h, e := m.Handler(strings.Repeat("x", 32))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		method, path, token string
		status              int
	}{{"GET", "/metrics", "", 401}, {"GET", "/metrics", "wrong", 401}, {"GET", "/metrics?token=secret", strings.Repeat("x", 32), 400}, {"POST", "/metrics", strings.Repeat("x", 32), 405}, {"GET", "/pprof", strings.Repeat("x", 32), 404}, {"GET", "/metrics", strings.Repeat("x", 32), 200}} {
		r := httptest.NewRequest(v.method, "http://metrics"+v.path, nil)
		if v.token != "" {
			r.Header.Set("Authorization", "Bearer "+v.token)
		}
		r.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("x", 32)})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != v.status || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal(w.Code, w.Header(), v)
		}
	}
}

func TestMetricsScrapeAdmissionIsBounded(t *testing.T) {
	m := New()
	entered := make(chan struct{}, 6)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "test_block", Help: "Test admission blocker."}, func() float64 { entered <- struct{}{}; <-release; return 0 }))
	h, e := m.Handler(strings.Repeat("x", 32))
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 2)
	for range 2 {
		go func() {
			r := httptest.NewRequest("GET", "http://metrics/metrics", nil)
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			done <- w.Code
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("scrapes did not enter collectors")
		}
	}
	r := httptest.NewRequest("GET", "http://metrics/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	unblock()
	for range 2 {
		select {
		case code := <-done:
			if code != 200 {
				t.Fatal(code)
			}
		case <-time.After(time.Second):
			t.Fatal("scrape did not finish")
		}
	}
}
