package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

func setup(t *testing.T) (*Server, *httptest.Server, *postgres.Store) {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := postgres.New(pool)
	s := New(store, Options{Origin: "http://trusted.example"})
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(); h.Close() })
	return s, h, store
}
func post(t *testing.T, url string, body any, want int) postgres.Credentials {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("POST %s: status %d", url, resp.StatusCode)
	}
	var c postgres.Credentials
	if want == 201 {
		if err = json.NewDecoder(resp.Body).Decode(&c); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func create(t *testing.T, url string) postgres.Credentials {
	return post(t, url+"/api/rooms", map[string]string{"host_name": "Ведущий"}, 201)
}
func join(t *testing.T, url string, h postgres.Credentials, name string) postgres.Credentials {
	return post(t, url+"/api/rooms/"+h.RoomID+"/participants", map[string]string{"invite_token": h.InviteToken, "name": name, "role": "player"}, 201)
}
func connect(t *testing.T, url string, c postgres.Credentials) (*websocket.Conn, Message) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/api/rooms/"+c.RoomID+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	if err = wsjson.Write(ctx, ws, map[string]any{"v": 1, "type": "authenticate", "token": c.Token}); err != nil {
		t.Fatal(err)
	}
	var m Message
	if err = wsjson.Read(ctx, ws, &m); err != nil {
		t.Fatal(err)
	}
	return ws, m
}
func write(t *testing.T, c *websocket.Conn, cmd game.Command) {
	t.Helper()
	cmd.Version = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, c, cmd); err != nil {
		t.Fatal(err)
	}
}
func readUntil(t *testing.T, c *websocket.Conn, match func(Message) bool) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var m Message
		if err := wsjson.Read(ctx, c, &m); err != nil {
			t.Fatal(err)
		}
		if match(m) {
			return m
		}
	}
}
func result(t *testing.T, c *websocket.Conn, id string) Message {
	t.Helper()
	return readUntil(t, c, func(m Message) bool { return (m.Type == "result" || m.Type == "error") && m.CommandID == id })
}
func TestWebSocketRoundReconnectAndRestart(t *testing.T) {
	s, h, store := setup(t)
	host := create(t, h.URL)
	var credentials []postgres.Credentials
	for i := 0; i < 12; i++ {
		credentials = append(credentials, join(t, h.URL, host, fmt.Sprintf("Игрок %d", i)))
	}
	hws, initial := connect(t, h.URL, host)
	if initial.Type != "snapshot" || len(initial.State.Participants) != 13 {
		t.Fatalf("initial: %+v", initial)
	}
	var players []*websocket.Conn
	for _, c := range credentials {
		ws, _ := connect(t, h.URL, c)
		players = append(players, ws)
	}
	write(t, hws, game.Command{ID: "open", Type: "open_question", QuestionID: "q1", Text: "2+2", Answer: "SECRET", Value: 100})
	if m := result(t, hws, "open"); m.Code != "ok" || m.State.Question.Answer != "SECRET" {
		t.Fatalf("open: %+v", m)
	}
	for _, ws := range players {
		m := readUntil(t, ws, func(m Message) bool { return m.State != nil && m.State.Phase == game.Open })
		if m.State.Question.Answer != "" {
			t.Fatal("answer leaked")
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for _, ws := range players {
		wg.Add(1)
		go func(ws *websocket.Conn) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			errs <- wsjson.Write(ctx, ws, game.Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q1"})
		}(ws)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winner := -1
	for i, ws := range players {
		m := result(t, ws, "buzz")
		if m.Code == "ok" {
			if winner != -1 {
				t.Fatal("two winners")
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatal("no winner")
	}
	correct := true
	cmd := game.Command{ID: "judge", Type: "judge", QuestionID: "q1", Correct: &correct}
	write(t, hws, cmd)
	first := result(t, hws, "judge")
	if first.Code != "ok" || first.State.Participants[winner+1].Score != 100 {
		t.Fatalf("score: %+v", first)
	}
	write(t, hws, cmd)
	duplicate := result(t, hws, "judge")
	if !duplicate.Duplicate || duplicate.ResultSeq != first.ResultSeq || duplicate.State.Participants[winner+1].Score != 100 {
		t.Fatalf("duplicate: %+v", duplicate)
	}
	players[winner].CloseNow()
	_, reconnected := connect(t, h.URL, credentials[winner])
	if reconnected.State.You != credentials[winner].ParticipantID || reconnected.State.Participants[winner+1].Score != 100 {
		t.Fatalf("reconnect: %+v", reconnected)
	}

	// A new server instance, using only persisted state, must preserve command receipts.
	s.Close()
	h.Close()
	if err := store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := New(postgres.New(store.Pool()), Options{})
	restarted := httptest.NewServer(next.Handler())
	t.Cleanup(func() { next.Close(); restarted.Close() })
	hostAgain, _ := connect(t, restarted.URL, host)
	write(t, hostAgain, cmd)
	m := result(t, hostAgain, "judge")
	if !m.Duplicate || m.ResultSeq != first.ResultSeq || m.State.Participants[winner+1].Score != 100 {
		t.Fatalf("restart: %+v", m)
	}
}

func TestHostDisconnectPausesAndResume(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	p := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	hws, _ := connect(t, h.URL, host)
	pws, _ := connect(t, h.URL, p)
	write(t, hws, game.Command{ID: "open", Type: "open_question", QuestionID: "q", Text: "Q", Answer: "A", Value: 100})
	result(t, hws, "open")
	hws.CloseNow()
	m := readUntil(t, pws, func(m Message) bool { return m.State != nil && m.State.Phase == game.Paused })
	if m.State.Question.Answer != "" {
		t.Fatal("answer leaked on pause")
	}
	newHost, _ := connect(t, h.URL, host)
	write(t, newHost, game.Command{ID: "resume", Type: "resume"})
	m = result(t, newHost, "resume")
	if m.Code != "ok" || m.State.Phase != game.Open {
		t.Fatalf("resume: %+v", m)
	}
}

func TestRetryRepairsUnbroadcastCommittedDecision(t *testing.T) {
	_, h, store := setup(t)
	host := create(t, h.URL)
	p1 := join(t, h.URL, host, "First")
	p2 := join(t, h.URL, host, "Second")
	hws, _ := connect(t, h.URL, host)
	first, _ := connect(t, h.URL, p1)
	second, _ := connect(t, h.URL, p2)
	write(t, hws, game.Command{ID: "open", Type: "open_question", QuestionID: "q", Text: "Q", Answer: "SECRET", Value: 100})
	result(t, hws, "open")
	for _, ws := range []*websocket.Conn{first, second} {
		readUntil(t, ws, func(m Message) bool { return m.State != nil && m.State.Phase == game.Open })
	}

	// Persist without distribution: the state peers see after a committed
	// command whose commit acknowledgement or broadcast was lost.
	cmd := game.Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	committed, err := store.Execute(ctx, host.RoomID, p1.ParticipantID, cmd)
	if err != nil || committed.Code != "ok" {
		t.Fatalf("commit: %+v, %v", committed, err)
	}
	write(t, first, cmd)
	retry := result(t, first, "buzz")
	if !retry.Duplicate || retry.Code != "ok" || retry.ResultSeq != committed.Seq || retry.State.Seq != committed.Seq {
		t.Fatalf("retry: %+v", retry)
	}
	for _, ws := range []*websocket.Conn{hws, second} {
		m := readUntil(t, ws, func(m Message) bool { return m.State != nil && m.State.Phase == game.Answering })
		if m.State.Winner != p1.ParticipantID || m.State.Seq != committed.Seq {
			t.Fatalf("peer did not recover committed decision: %+v", m)
		}
		if ws == second && m.State.Question.Answer != "" {
			t.Fatal("retry broadcast leaked answer")
		}
	}
}

func TestFailedDisconnectPauseSurvivesReconnect(t *testing.T) {
	_, h, store := setup(t)
	host := create(t, h.URL)
	player := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	hws, _ := connect(t, h.URL, host)
	pws, _ := connect(t, h.URL, player)
	write(t, hws, game.Command{ID: "open", Type: "open_question", QuestionID: "q", Text: "Q", Answer: "SECRET", Value: 100})
	result(t, hws, "open")
	readUntil(t, pws, func(m Message) bool { return m.State != nil && m.State.Phase == game.Open })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := store.Pool().Exec(ctx, `
		CREATE FUNCTION fail_pause() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.kind = 'paused' THEN RAISE EXCEPTION 'pause persistence failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER fail_pause BEFORE INSERT ON game_events FOR EACH ROW EXECUTE FUNCTION fail_pause();
	`)
	if err != nil {
		t.Fatal(err)
	}
	hws.CloseNow()
	var closed Message
	if err = wsjson.Read(ctx, pws, &closed); err == nil || ctx.Err() != nil {
		t.Fatalf("failed pause must close player connection: message=%+v, err=%v, context=%v", closed, err, ctx.Err())
	}
	// While persistence still fails, reconnect must not expose an active room.
	blocked, unavailable := connect(t, h.URL, host)
	blocked.CloseNow()
	if unavailable.Type != "error" || unavailable.Code != "unavailable" {
		t.Fatalf("reconnect bypassed failed pause: %+v", unavailable)
	}
	if _, err = store.Pool().Exec(ctx, "DROP TRIGGER fail_pause ON game_events"); err != nil {
		t.Fatal(err)
	}
	newPlayer, paused := connect(t, h.URL, player)
	if paused.State == nil || paused.State.Phase != game.Paused || paused.State.Question.Answer != "" {
		t.Fatalf("player reconnect: %+v", paused)
	}
	newHost, pausedHost := connect(t, h.URL, host)
	if pausedHost.State == nil || pausedHost.State.Phase != game.Paused || pausedHost.State.Seq != paused.State.Seq {
		t.Fatalf("host reconnect must preserve pause: %+v", pausedHost)
	}
	write(t, newPlayer, game.Command{ID: "too-early", Type: "buzz", QuestionID: "q"})
	if m := result(t, newPlayer, "too-early"); m.Code == "ok" || m.State == nil || m.State.Phase != game.Paused {
		t.Fatalf("command resumed room without host decision: %+v", m)
	}
	write(t, newHost, game.Command{ID: "resume", Type: "resume"})
	if m := result(t, newHost, "resume"); m.Code != "ok" || m.State == nil || m.State.Phase != game.Open {
		t.Fatalf("explicit resume: %+v", m)
	}
}

func TestAuthOriginAndMalformedCommands(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	p := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(h.URL, "http") + "/api/rooms/" + host.RoomID + "/ws"
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("origin: %v %+v", err, resp)
	}
	bad := host
	bad.Token = strings.Repeat("x", 64)
	_, m := connect(t, h.URL, bad)
	if m.Code != "unauthorized" {
		t.Fatalf("bad auth: %+v", m)
	}
	connect(t, h.URL, host)
	pws, _ := connect(t, h.URL, p)
	write(t, pws, game.Command{ID: "finish", Type: "finish"})
	m = result(t, pws, "finish")
	if m.Code != "forbidden" {
		t.Fatalf("player became host: %+v", m)
	}
	if err = wsjson.Write(ctx, pws, map[string]any{"v": 1, "id": "inject", "type": "finish", "actor_id": host.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	m = readUntil(t, pws, func(m Message) bool { return m.Type == "error" })
	if m.Code != "invalid_json" {
		t.Fatalf("unknown field: %+v", m)
	}
	other := create(t, h.URL)
	other.Token = p.Token
	_, m = connect(t, h.URL, other)
	if m.Code != "unauthorized" {
		t.Fatal("cross-room token accepted")
	}
}

func TestOutboxAndRateLimits(t *testing.T) {
	box := newOutbox()
	data := make([]byte, 32768)
	if !box.push(data) || !box.push(data) || box.push([]byte("overflow")) {
		t.Fatal("outbox byte limit")
	}
	<-box.messages
	box.sent(len(data))
	if !box.push(data) {
		t.Fatal("outbox capacity not released")
	}
	b := newBucket()
	now := time.Now()
	for i := 0; i < 20; i++ {
		if !b.allow(now) {
			t.Fatalf("burst stopped at %d", i)
		}
	}
	if b.allow(now) {
		t.Fatal("unlimited burst")
	}
	if !b.allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("tokens not replenished")
	}
}

func TestConnectionLimitHasExplicitError(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	connect(t, h.URL, host)
	connect(t, h.URL, host)
	_, m := connect(t, h.URL, host)
	if m.Type != "error" || m.Code != "connection_limit" {
		t.Fatalf("limit: %+v", m)
	}
}

func TestMalformedFloodClosesConnection(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	ws, _ := connect(t, h.URL, host)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 25; i++ {
		if err := ws.Write(ctx, websocket.MessageText, []byte(`{"invalid":true}`)); err != nil {
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			return
		}
		var m Message
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			return
		}
	}
	t.Fatal("malformed commands bypassed rate limit")
}
