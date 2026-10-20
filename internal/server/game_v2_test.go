package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func sendV2(t *testing.T, app *Server, conn *websocket.Conn, actor postgres.Credentials, id, kind string, data any) Message {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := app.store.Load(context.Background(), actor.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	cmd := game.Command{Version: 2, ID: id, Type: kind, Data: raw}
	p, _ := state.Participant(actor.ParticipantID)
	if p.Role == game.Host {
		cmd.ExpectedSeq = &state.Seq
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = wsjson.Write(ctx, conn, cmd); err != nil {
		t.Fatal(err)
	}
	response := result(t, conn, id)
	if response.Code != "ok" {
		t.Fatalf("%s failed: %+v", kind, response)
	}
	return response
}

func TestV2RuntimeTimerAndHostTransfer(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "runtime_author", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host"}, 201), &host)
	p := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	var spectator postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/participants", "", map[string]any{"name": "Next host", "role": "spectator", "invite_token": host.InviteToken}, 201), &spectator)
	hc, initial := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, p)
	sc, _ := connect(t, h.URL, spectator)
	if initial.State.Gameplay == nil {
		t.Fatal("production room missing v2 rules")
	}
	// Trusted package loading remains an HTTP server action, even for the host.
	payload, _ := json.Marshal(game.BeginRoundPayload{Round: game.RoundSpec{ID: "forged"}})
	if err = wsjson.Write(ctx, hc, game.Command{Version: 2, ID: "forged", Type: "begin_round", Data: payload}); err != nil {
		t.Fatal(err)
	}
	if m := result(t, hc, "forged"); m.Code != "forbidden" {
		t.Fatal("client supplied round accepted", m.Code)
	}
	pin, doc, err := app.roomDocument(ctx, host.RoomID)
	_ = pin
	if err != nil {
		t.Fatal(err)
	}
	path := h.URL + "/api/rooms/" + host.RoomID
	api(t, "POST", path+"/round", host.Token, map[string]any{"round_id": doc.Rounds[0].ID, "command_id": "start", "expected_seq": initial.State.Seq}, 200)
	state, err := app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	api(t, "POST", path+"/select", host.Token, map[string]any{"question_id": doc.Rounds[0].Themes[0].Questions[0].ID, "command_id": "choose", "expected_seq": state.Seq}, 200)
	state, _ = app.store.Load(ctx, host.RoomID)
	stage := state.Gameplay.Stage
	sendV2(t, app, hc, host, "open", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	// A persisted deadline is scheduled once by the actor, without DB polling.
	rm, err := app.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rm.do(ctx, func(ctx context.Context, rm *room) (any, error) {
		state, e := app.store.Load(ctx, host.RoomID)
		if e != nil {
			return nil, e
		}
		state.Gameplay.Stage.DeadlineMS = time.Now().Add(50 * time.Millisecond).UnixMilli()
		raw, e := json.Marshal(state)
		if e != nil {
			return nil, e
		}
		_, e = app.store.Pool().Exec(ctx, "UPDATE rooms SET snapshot=$2 WHERE id=$1", host.RoomID, raw)
		if e != nil {
			return nil, e
		}
		rm.broadcast(state, nil)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	expired := readUntil(t, pc, func(m Message) bool {
		return m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Stage.Expired
	})
	if expired.State.Gameplay.Question.Answer != nil {
		t.Fatal("timer leaked right answer")
	}
	sendV2(t, app, hc, host, "pause", "pause", game.EmptyPayload{})
	sendV2(t, app, hc, host, "transfer", "transfer_host", game.TransferPayload{TargetID: spectator.ParticipantID})
	sendV2(t, app, sc, spectator, "resume", "resume", game.EmptyPayload{})
	hc.CloseNow()
	time.Sleep(100 * time.Millisecond)
	state, err = app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase == game.Paused {
		t.Fatal("former host disconnect paused new host")
	}
	if p, _ := state.Participant(host.ParticipantID); p.Role != game.Spectator {
		t.Fatal("old host not demoted")
	}
	sendV2(t, app, sc, spectator, "finish", "finish", game.EmptyPayload{})
	ended := readUntil(t, pc, func(m Message) bool { return m.State != nil && m.State.Phase == game.Finished })
	if ended.State.Gameplay.Question.Answer != nil {
		t.Fatal("finish automatically disclosed private answer")
	}
	api(t, "GET", path+"/results?limit=100", p.Token, nil, 200)
}

func TestV2RuntimeBroadcastsFalseStartExpiry(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "false_start_author", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	rules := game.DefaultRules()
	rules.FalseStartSeconds = 1
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "rules": rules}, 201), &host)
	player := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	hc, initial := connect(t, h.URL, host)
	pc, _ := connect(t, h.URL, player)
	path := h.URL + "/api/rooms/" + host.RoomID
	api(t, "POST", path+"/round", host.Token, map[string]any{"round_id": "r1", "command_id": "start", "expected_seq": initial.State.Seq}, 200)
	state, err := app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	api(t, "POST", path+"/select", host.Token, map[string]any{"question_id": "r1-t1-q1", "command_id": "choose", "expected_seq": state.Seq}, 200)
	state, _ = app.store.Load(ctx, host.RoomID)
	stage := state.Gameplay.Stage
	sendV2(t, app, pc, player, "early", "buzz", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	opened := sendV2(t, app, hc, host, "open", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	deadline := opened.State.Gameplay.Stage.DeadlineMS
	readCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	for {
		var m Message
		if err := wsjson.Read(readCtx, pc, &m); err != nil {
			t.Fatalf("false-start expiry did not broadcast permission: %v", err)
		}
		if m.State == nil || m.State.Seq <= opened.State.Seq {
			continue
		}
		for _, action := range m.State.Gameplay.AllowedActions {
			if action.Type == "buzz" {
				if m.State.Gameplay.Stage.Kind != "buzz_open" || m.State.Gameplay.Stage.DeadlineMS != deadline {
					t.Fatal("unblock changed question deadline")
				}
				sendV2(t, app, pc, player, "valid-buzz", "buzz", game.StagePayload{StageID: m.State.Gameplay.Stage.ID, InstanceID: stage.InstanceID})
				return
			}
		}
	}
}
