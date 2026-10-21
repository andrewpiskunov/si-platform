package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"net/http/httptest"
	"siplatform/internal/game"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoomExpiryEvictsActorAndTerminatesSocket(t *testing.T) {
	s, h, store := setup(t)
	host := create(t, h.URL)
	ws, _ := connect(t, h.URL, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rm, err := s.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	s.appearanceMu.Lock()
	s.appearanceKnown[host.RoomID] = "known"
	s.appearanceMu.Unlock()
	if _, err = store.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	if err = s.sweepRooms(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	terminal := readUntil(t, ws, func(m Message) bool { return m.Type == "error" })
	if terminal.Code != "room_expired" {
		t.Fatalf("terminal %+v", terminal)
	}
	if _, err = rm.do(ctx, func(context.Context, *room) (any, error) { t.Error("retired job ran"); return nil, nil }); game.Code(err) != "room_expired" {
		t.Fatalf("old actor: %v", err)
	}
	if _, err = s.getRoom(ctx, host.RoomID); game.Code(err) != "not_found" {
		t.Fatalf("reload: %v", err)
	}
	s.mu.Lock()
	cached := s.rooms[host.RoomID]
	s.mu.Unlock()
	if cached != nil {
		t.Fatal("actor cached")
	}
	s.appearanceMu.Lock()
	_, known := s.appearanceKnown[host.RoomID]
	s.appearanceMu.Unlock()
	if known {
		t.Fatal("appearance cached")
	}
}
func TestRoomRetentionParticipantOnly(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	for _, token := range []string{"", host.Token} {
		req, _ := http.NewRequest("GET", h.URL+"/api/rooms/"+host.RoomID+"/retention", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if token == "" {
			if resp.StatusCode != 401 {
				t.Fatalf("anonymous: %d", resp.StatusCode)
			}
		} else {
			if resp.StatusCode != 200 {
				t.Fatalf("participant: %d", resp.StatusCode)
			}
			var result map[string]string
			if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"created_at", "last_activity_at", "expires_at"} {
				if _, err = time.Parse(time.RFC3339Nano, result[key]); err != nil {
					t.Fatalf("%s: %v", key, err)
				}
			}
		}
		resp.Body.Close()
	}
}

func TestRoomExpiryConcurrentRequestsCannotReviveActor(t *testing.T) {
	s, h, store := setup(t)
	host := create(t, h.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rm, err := s.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool().Exec(ctx, `UPDATE rooms SET last_activity_at=now()-interval '8 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			actor, err := s.getRoom(ctx, host.RoomID)
			if err != nil {
				if game.Code(err) != "not_found" {
					t.Errorf("get %v", err)
				}
				return
			}
			_, err = actor.do(ctx, func(ctx context.Context, r *room) (any, error) {
				if i%2 == 0 {
					return store.Join(ctx, r.id, host.InviteToken, "Late", game.Spectator)
				}
				return store.Execute(ctx, r.id, host.ParticipantID, game.Command{Version: 1, ID: fmt.Sprintf("late-%d", i), Type: "finish"})
			})
			if err == nil {
				t.Error("expired action accepted")
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.sweepRooms(ctx, time.Now()); err != nil {
			t.Errorf("sweep %v", err)
		}
	}()
	wg.Wait()
	select {
	case <-rm.retirementDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = s.getRoom(ctx, host.RoomID); game.Code(err) != "not_found" {
		t.Fatalf("revived: %v", err)
	}
}

func TestRoomExpiryFailureClosesActorAndRetries(t *testing.T) {
	s, h, store := setup(t)
	host := create(t, h.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rm, err := s.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool().Exec(ctx, `CREATE FUNCTION fail_room_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test'; END $$;
 CREATE TRIGGER fail_room_delete BEFORE DELETE ON rooms FOR EACH ROW EXECUTE FUNCTION fail_room_delete()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	if err = s.sweepRooms(ctx, time.Now()); err == nil {
		t.Fatal("deletion failure hidden")
	}
	if !rm.stopped.Load() {
		t.Fatal("failed retirement left actor alive")
	}
	var exists bool
	if err = store.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rooms WHERE id=$1)`, host.RoomID).Scan(&exists); err != nil || !exists {
		t.Fatalf("rollback %v %v", exists, err)
	}
	if _, err = store.Pool().Exec(ctx, `DROP TRIGGER fail_room_delete ON rooms`); err != nil {
		t.Fatal(err)
	}
	if err = s.sweepRooms(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rooms WHERE id=$1)`, host.RoomID).Scan(&exists); err != nil || exists {
		t.Fatalf("retry %v %v", exists, err)
	}
}

func TestRoomExpiryLostCommitReplyModel(t *testing.T) {
	s, h, store := setup(t)
	host := create(t, h.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rm, err := s.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '91 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	// Model a successful server-side COMMIT whose reply was lost: the retirement
	// caller takes exactly the error cleanup path despite durable deletion.
	j := job{ctx: ctx, result: make(chan reply, 1), run: func(ctx context.Context, r *room) (any, error) {
		retired, err := store.RetireRoom(ctx, r.id, time.Now())
		if err != nil || !retired {
			return nil, fmt.Errorf("retire %v %v", retired, err)
		}
		lost := errors.New("commit reply lost")
		r.stopForExpiry(false, lost)
		return nil, lost
	}}
	rm.requests <- j
	if result := <-j.result; result.err == nil {
		t.Fatal("model did not take failure path")
	}
	if !rm.stopped.Load() {
		t.Fatal("actor survived uncertain commit")
	}
	if _, err = s.getRoom(ctx, host.RoomID); game.Code(err) != "not_found" {
		t.Fatalf("reloaded deleted room: %v", err)
	}
	if _, err = rm.do(ctx, func(context.Context, *room) (any, error) { t.Error("stale actor executed"); return nil, nil }); game.Code(err) != "room_expired" {
		t.Fatalf("stale request %v", err)
	}
}

func TestRoomExpiryTerminalDiscardsFullQueue(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				c := &client{ctx: ctx, cancel: cancel, conn: conn, out: newOutbox()}
				for i := 0; i < 64; i++ {
					if !c.send(Message{Type: "stale"}) {
						t.Error("queue filling failed")
						return
					}
				}
				if expired {
					c.expireRoom()
				} else {
					c.revoke()
				}
				c.writer()
			}))
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			var m Message
			if err = wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			want := "unauthorized"
			if expired {
				want = "room_expired"
			}
			if m.Type != "error" || m.Code != want {
				t.Fatalf("queued frame leaked: %+v", m)
			}
			if err = wsjson.Read(ctx, conn, &m); err == nil {
				t.Fatalf("extra frame: %+v", m)
			}
		})
	}
}

func TestRoomAdmissionGateIsPerRoomAndCancelable(t *testing.T) {
	s, h, _ := setup(t)
	one := create(t, h.URL)
	two := create(t, h.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := s.lockRoomAdmission(ctx, one.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.getRoom(ctx, two.RoomID); err != nil {
		unlock()
		t.Fatalf("unrelated admission blocked: %v", err)
	}
	blocked, cancelBlocked := context.WithCancel(ctx)
	cancelBlocked()
	if _, err = s.getRoom(blocked, one.RoomID); !errors.Is(err, context.Canceled) {
		unlock()
		t.Fatalf("blocked admission cancellation: %v", err)
	}
	unlock()
	if _, err = s.getRoom(ctx, one.RoomID); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	remaining := len(s.roomGates)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("gate leak: %d", remaining)
	}
}
