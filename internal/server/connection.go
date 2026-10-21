package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
	"siplatform/internal/telemetry"
)

type Message struct {
	Version    int        `json:"v"`
	Type       string     `json:"type"`
	CommandID  string     `json:"command_id,omitempty"`
	Code       string     `json:"code,omitempty"`
	ResultSeq  int64      `json:"result_seq,omitempty"`
	Duplicate  bool       `json:"duplicate,omitempty"`
	ServerTime int64      `json:"server_time"`
	State      *game.View `json:"state,omitempty"`
}
type outbox struct {
	mu       sync.Mutex
	bytes    int
	messages chan []byte
}

func newOutbox() *outbox { return &outbox{messages: make(chan []byte, 64)} }
func (b *outbox) push(data []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bytes+len(data) > 64<<10 {
		return false
	}
	select {
	case b.messages <- data:
		b.bytes += len(data)
		return true
	default:
		return false
	}
}
func (b *outbox) sent(size int) { b.mu.Lock(); b.bytes -= size; b.mu.Unlock() }

type client struct {
	metrics        *telemetry.Metrics
	revoked        atomic.Bool
	expired        atomic.Bool
	appearanceOnly bool
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	conn           *websocket.Conn
	out            *outbox
}

func (c *client) send(m Message) bool {
	if c.ctx.Err() != nil || c.revoked.Load() {
		return false
	}
	m.Version, m.ServerTime = 1, time.Now().UnixMilli()
	data, err := json.Marshal(m)
	if err != nil {
		c.cancel()
		return false
	}
	if !c.out.push(data) {
		if c.metrics != nil {
			c.metrics.SlowClient()
		}
		c.cancel()
		return false
	}
	return true
}
func (c *client) expireRoom() {
	c.expired.Store(true)
	c.revoke()
}
func (c *client) revoke() {
	if c.revoked.Swap(true) {
		return
	}
	// Wake the writer; it discards pending frames and sends one terminal error.
	// A full queue already wakes the writer. Never cancel before its terminal
	// write: it must discard the queued frames and deliver the terminal code.
	_ = c.out.push([]byte{})
}
func (c *client) writer() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case b := <-c.out.messages:
			c.out.sent(len(b))
			if c.revoked.Load() {
				code := "unauthorized"
				if c.expired.Load() {
					code = "room_expired"
				}
				terminal, _ := json.Marshal(Message{Version: 1, Type: "error", Code: code, ServerTime: time.Now().UnixMilli()})
				ctx, cancel := context.WithTimeout(c.ctx, time.Second)
				_ = c.conn.Write(ctx, websocket.MessageText, terminal)
				cancel()
				c.cancel()
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
			err := c.conn.Write(ctx, websocket.MessageText, b)
			if err == nil && c.metrics != nil {
				c.metrics.WSBytes(len(b))
			}
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}
func (c *client) heartbeat() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("appearance_only")
	if mode != "" && mode != "1" {
		httpError(w, game.Fault("invalid_input"))
		return
	}
	if !s.track() {
		httpError(w, game.Fault("unavailable"))
		return
	}
	defer s.wg.Done()
	select {
	case s.connections <- struct{}{}:
		defer func() { <-s.connections }()
	default:
		httpError(w, game.Fault("busy"))
		return
	}
	patterns := []string(nil)
	if s.options.Origin != "" {
		patterns = []string{s.options.Origin}
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(16 << 10)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
	var auth struct {
		Version int    `json:"v"`
		Type    string `json:"type"`
		Token   string `json:"token"`
	}
	kind, data, err := ws.Read(authCtx)
	if err == nil && kind != websocket.MessageText {
		err = game.Fault("invalid_json")
	}
	if err == nil {
		err = strictJSON(bytes.NewReader(data), &auth)
	}
	if err == nil && (auth.Version != 1 || auth.Type != "authenticate") {
		err = game.Fault("unauthorized")
	}
	var id string
	if err == nil {
		id, err = s.store.Authenticate(authCtx, chi.URLParam(r, "room"), auth.Token)
	}
	authCancel()
	if err != nil {
		errorCtx, errorCancel := context.WithTimeout(ctx, time.Second)
		defer errorCancel()
		_ = wsjson.Write(errorCtx, ws, Message{Version: 1, Type: "error", Code: game.Code(err), ServerTime: time.Now().UnixMilli()})
		return
	}
	c := &client{metrics: s.metrics, appearanceOnly: mode == "1", id: id, ctx: ctx, cancel: cancel, conn: ws, out: newOutbox()}
	loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Second)
	rm, err := s.getRoom(loadCtx, chi.URLParam(r, "room"))
	if err == nil {
		defer rm.detach(c)
		err = rm.attach(loadCtx, c)
	}
	loadCancel()
	if err != nil {
		errorCtx, errorCancel := context.WithTimeout(ctx, time.Second)
		_ = wsjson.Write(errorCtx, ws, Message{Version: 1, Type: "error", Code: game.Code(err), ServerTime: time.Now().UnixMilli()})
		errorCancel()
		return
	}
	written := make(chan struct{})
	pinged := make(chan struct{})
	go func() { defer close(written); c.writer() }()
	go func() { defer close(pinged); c.heartbeat() }()
	defer func() { cancel(); _ = ws.CloseNow(); <-written; <-pinged }()
	inbound := newBucket()
	for {
		kind, b, err := ws.Read(ctx)
		if err != nil {
			return
		}
		// Bound parsing and error responses too, including malformed or binary frames.
		if !inbound.allow(time.Now()) {
			return
		}
		var cmd game.Command
		if kind != websocket.MessageText {
			c.send(Message{Type: "error", Code: "invalid_json"})
			continue
		}
		if err = strictJSON(bytes.NewReader(b), &cmd); err != nil {
			c.send(Message{Type: "error", Code: "invalid_json"})
			continue
		}
		if err = cmd.Validate(); err != nil {
			id := ""
			if game.ValidID(cmd.ID) {
				id = cmd.ID
			}
			c.send(Message{Type: "error", CommandID: id, Code: game.Code(err)})
			continue
		}
		// Configured rooms select immutable package questions on the server.
		if c.appearanceOnly || (s.options.Library != nil && cmd.Type == "open_question") || cmd.Type == "begin_round" || cmd.Type == "begin_question" || cmd.Type == "set_appearance" {
			c.send(Message{Type: "error", CommandID: cmd.ID, Code: "forbidden"})
			continue
		}
		if err = rm.command(ctx, c, cmd); err != nil {
			c.send(Message{Type: "error", CommandID: cmd.ID, Code: game.Code(err)})
			if game.Code(err) == "ownership_lost" {
				s.cancel()
				return
			}
		}
	}
}
