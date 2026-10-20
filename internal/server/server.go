package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

type Options struct{ Origin string }
type Server struct {
	store       *postgres.Store
	options     Options
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	rooms       map[string]*room
	wg          sync.WaitGroup
	connections chan struct{}
	httpMu      sync.Mutex
	httpLimit   *bucket
}

func New(store *postgres.Store, options Options) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{store: store, options: options, ctx: ctx, cancel: cancel, rooms: make(map[string]*room), connections: make(chan struct{}, 10000), httpLimit: newBucket()}
}
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *Server) track() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if s.ctx.Err() != nil || s.store.Pool().Ping(ctx) != nil {
			writeJSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})
	r.Route("/api", func(r chi.Router) {
		r.Use(s.guard)
		r.Post("/rooms", s.create)
		r.Post("/rooms/{room}/participants", s.join)
		r.Get("/rooms/{room}/ws", s.websocket)
	})
	return r
}
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ctx.Err() != nil {
			httpError(w, game.Fault("unavailable"))
			return
		}
		if !s.validOrigin(r) {
			httpError(w, game.Fault("forbidden"))
			return
		}
		s.httpMu.Lock()
		// Global admission limiter: 100 requests/s, burst 200. Participant commands have separate limits.
		now := time.Now()
		allowed := s.httpLimit.take(now, 100, 200)
		s.httpMu.Unlock()
		if !allowed {
			httpError(w, game.Fault("rate_limited"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	} // CLI clients have no browser Origin.
	expected := s.options.Origin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	return origin == expected
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return game.Fault("invalid_json")
	}
	return strictJSON(http.MaxBytesReader(w, r.Body, 16<<10), v)
}
func strictJSON(reader io.Reader, v any) error {
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return game.Fault("invalid_json")
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return game.Fault("invalid_json")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func httpError(w http.ResponseWriter, err error) {
	code := game.Code(err)
	status := 400
	switch code {
	case "unauthorized":
		status = 401
	case "forbidden":
		status = 403
	case "not_found":
		status = 404
	case "room_full", "room_limit", "question_in_progress", "finished":
		status = 409
	case "rate_limited", "busy":
		status = 429
	case "unavailable", "ownership_lost":
		status = 503
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostName string `json:"host_name"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c, err := s.store.Create(ctx, req.HostName)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 201, c)
}
func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string    `json:"name"`
		Role   game.Role `json:"role"`
		Invite string    `json:"invite_token"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rm, err := s.getRoom(ctx, chi.URLParam(r, "room"))
	if err != nil {
		httpError(w, err)
		return
	}
	value, err := rm.do(ctx, func(ctx context.Context, rm *room) (any, error) {
		c, err := s.store.Join(ctx, rm.id, req.Invite, req.Name, req.Role)
		if err != nil {
			return nil, err
		}
		state, err := s.store.Load(ctx, rm.id)
		if err == nil {
			rm.broadcast(state, nil)
		}
		// A committed join must return its credentials even if the follow-up read fails.
		return c, nil
	})
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, 201, value)
}
func (s *Server) getRoom(ctx context.Context, id string) (*room, error) {
	if len(id) != 32 || !game.ValidID(id) {
		return nil, game.Fault("not_found")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, game.Fault("unavailable")
	}
	existing := s.rooms[id]
	s.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	if _, err := s.store.Load(ctx, id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, game.Fault("unavailable")
	}
	if existing = s.rooms[id]; existing != nil {
		return existing, nil
	}
	rm := &room{id: id, server: s, requests: make(chan job, 128), clients: make(map[*client]struct{}), limits: make(map[string]*bucket)}
	s.rooms[id] = rm
	s.wg.Add(1)
	go func() { defer s.wg.Done(); rm.run() }()
	return rm, nil
}
