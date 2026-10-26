package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/ai"
	"siplatform/internal/blob"
	"siplatform/internal/game"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/telemetry"
	"siplatform/internal/themes"
)

type Options struct {
	Metrics         *telemetry.Metrics
	ExportDirectory string
	Origin          string
	Accounts        *accounts.Service
	AI              *ai.Service
	Library         *library.Service
	Objects         blob.Store
	Uploads         *ingest.Service
	Themes          *themes.Service
}
type Server struct {
	metrics         *telemetry.Metrics
	store           *postgres.Store
	options         Options
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	closed          bool
	rooms           map[string]*room
	roomGates       map[string]*roomGate
	wg              sync.WaitGroup
	connections     chan struct{}
	exports         chan struct{}
	httpMu          sync.Mutex
	httpLimit       *bucket
	mediaSecret     [32]byte
	appearanceMu    sync.Mutex
	appearanceKnown map[string]string
}

func New(store *postgres.Store, options Options) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: store, options: options, ctx: ctx, cancel: cancel, rooms: make(map[string]*room), roomGates: make(map[string]*roomGate), connections: make(chan struct{}, 10000), exports: make(chan struct{}, 2), httpLimit: newBucket()}
	if options.Metrics == nil {
		s.metrics = telemetry.New()
	} else {
		s.metrics = options.Metrics
	}
	s.metrics.SetLive(s.liveMetrics)
	// Tickets are short lived and intentionally expire on a server restart.
	// crypto/rand.Read is guaranteed to fill the buffer or terminate in Go 1.24+.
	_, _ = rand.Read(s.mediaSecret[:])
	s.appearanceKnown = make(map[string]string)
	if options.Themes != nil {
		s.wg.Add(1)
		go s.watchAppearances()
	}
	s.wg.Add(1)
	go s.watchRoomExpiry()
	return s
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
	r.Use(s.measureHTTP)
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
		s.platformRoutes(r)
		r.Post("/rooms", s.create)
		r.Get("/rooms", s.publicRooms)
		r.Get("/my-rooms", s.myRooms)
		r.Post("/my-rooms/{room}/session", s.recoverMyRoom)
		r.Post("/rooms/{room}/participants", s.join)
		r.Get("/rooms/{room}/ws", s.websocket)
		r.Post("/rooms/{room}/invite", s.rotateInvite)
		r.Get("/rooms/{room}/results", s.roomResults)
		r.Get("/rooms/{room}/retention", s.roomRetention)
		r.Get("/rooms/{room}/roster", s.roomRoster)
		r.Post("/rooms/{room}/session", s.bindRoomSession)
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
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// Chunk bodies and editor saves must not hold database locks forever
			// when the sender stops producing bytes. WebSocket reads use their
			// own authentication/heartbeat deadlines.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
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
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	} // CLI clients have no browser Origin.
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin := origins[0]
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
	case "forbidden", "blocked", "guest_banned":
		status = 403
	case "not_found":
		status = 404
	case "guest_session_mismatch", "guest_session_unavailable", "room_full", "room_limit", "question_in_progress", "finished", "stale_state", "command_conflict", "question_reused":
		status = 409
	case "rate_limited", "busy":
		status = 429
	case "unavailable", "ownership_lost":
		status = 503
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if s.options.Accounts != nil {
		s.createGame(w, r)
		return
	}
	var req struct {
		HostName string `json:"host_name"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	guest := guestCookie(r)
	c, err := s.store.CreateWithGuest(ctx, req.HostName, guest)
	if err != nil {
		httpError(w, err)
		return
	}
	s.setGuestCookie(w, r, c.RoomID, guest)
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
	guest := guestCookie(r)
	value, err := rm.do(ctx, func(ctx context.Context, rm *room) (any, error) {
		c, err := s.store.JoinWithGuest(ctx, rm.id, req.Invite, req.Name, req.Role, guest)
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
	s.setGuestCookie(w, r, rm.id, guest)
	writeJSON(w, 201, value)
}
func (s *Server) getRoom(ctx context.Context, id string) (*room, error) {
	if len(id) != 32 || !game.ValidID(id) {
		return nil, game.Fault("not_found")
	}
	unlock, err := s.lockRoomAdmission(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, game.Fault("unavailable")
	}
	existing := s.rooms[id]
	s.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	// Per-room admission serializes load/publication with uncached retirement.
	// Database work never holds the global map mutex.
	if _, err = s.store.Load(ctx, id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, game.Fault("unavailable")
	}
	rm := &room{id: id, server: s, requests: make(chan job, 128), done: make(chan struct{}), retirementDone: make(chan struct{}), clients: make(map[*client]struct{}), limits: make(map[string]*bucket)}
	s.rooms[id] = rm
	s.wg.Add(1)
	go func() { defer s.wg.Done(); rm.run() }()
	return rm, nil
}
