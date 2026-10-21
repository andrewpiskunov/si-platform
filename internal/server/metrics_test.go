package server

import (
	"context"
	"net/http/httptest"
	"siplatform/internal/game"
	"strings"
	"testing"
	"time"
)

func metricText(t *testing.T, s *Server) string {
	t.Helper()
	h, e := s.Metrics().Handler(strings.Repeat("m", 32))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://metrics/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	return w.Body.String()
}
func TestHTTPMetricsNoPrivateURLsAndNoPublicEndpoint(t *testing.T) {
	app, h, _ := platform(t)
	api(t, "GET", h.URL+"/api/catalogue?q=private-question-text", "", nil, 200)
	for range 3 {
		api(t, "GET", h.URL+"/api/secret-owner-id", "", nil, 404)
	}
	api(t, "GET", h.URL+"/metrics", "", nil, 404)
	text := metricText(t, app)
	if !strings.Contains(text, `si_http_requests_total{category="catalogue",method="GET",status="2xx"} 1`) || !strings.Contains(text, `si_http_requests_total{category="other",method="GET",status="4xx"} 4`) {
		t.Fatal(text)
	}
	for _, secret := range []string{"private-question-text", "secret-owner-id", "/api/"} {
		if strings.Contains(text, secret) {
			t.Fatal("private labels", secret)
		}
	}
}
func TestRoomJobMetricsMeasureQueueAndExecution(t *testing.T) {
	app, _, _ := platform(t)
	rm := &room{server: app, id: "private-room", requests: make(chan job, 2), done: make(chan struct{}), clients: map[*client]struct{}{}}
	done := make(chan struct{})
	go func() { defer close(done); rm.run() }()
	defer func() { close(rm.done); <-done }()
	result := make(chan reply, 1)
	rm.requests <- job{ctx: context.Background(), submitted: time.Now().Add(-time.Second), kind: "command", result: result, run: func(context.Context, *room) (any, error) { return nil, game.Fault("conflict") }}
	<-result
	text := metricText(t, app)
	if !strings.Contains(text, `si_room_processing_seconds_count{kind="command",outcome="error"} 1`) || !strings.Contains(text, `si_room_queue_seconds_bucket{kind="command",outcome="error",le="0.3"} 0`) {
		t.Fatal(text)
	}
	if strings.Contains(text, "private-room") {
		t.Fatal("room label leaked")
	}
}

func TestRealWebSocketMetricsPreserveUpgradeAndPrivateAnswers(t *testing.T) {
	app, h, _ := setup(t)
	host := create(t, h.URL)
	join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Second")
	ws, _ := connect(t, h.URL, host)
	write(t, ws, game.Command{ID: "metric-open", Type: "open_question", QuestionID: "q1", Text: "Private prompt", Answer: "Private answer", Value: 100})
	if m := result(t, ws, "metric-open"); m.Code != "ok" {
		t.Fatal(m.Code)
	}
	text := metricText(t, app)
	for _, want := range []string{`si_ws_connections 1`, `si_rooms_loaded 1`, `si_room_processing_seconds_count{kind="command",outcome="ok"} 1`, `si_ws_sent_bytes_total`} {
		if !strings.Contains(text, want) {
			t.Fatal("missing", want)
		}
	}
	for _, secret := range []string{host.Token, host.RoomID, "Private prompt", "Private answer"} {
		if strings.Contains(text, secret) {
			t.Fatal("private data in metrics")
		}
	}
}
