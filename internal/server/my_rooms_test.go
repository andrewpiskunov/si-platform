package server

import (
	"context"
	"encoding/json"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"testing"
)

func TestMyRoomsRecoveryOwnershipAndCurrentRole(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "rooms_owner", "long-enough-password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "rooms_other", "long-enough-password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "title": "Private evening"}, 201), &host)
	endpoint := h.URL + "/api/my-rooms"
	api(t, "GET", endpoint, "", nil, 401)
	raw := api(t, "GET", endpoint, owner.Token, nil, 200)
	var list struct {
		Items []struct {
			RoomID    string    `json:"room_id"`
			Role      game.Role `json:"role"`
			CanReturn bool      `json:"can_return"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].RoomID != host.RoomID || list.Items[0].Role != game.Host || !list.Items[0].CanReturn {
		t.Fatalf("list: %s", raw)
	}
	json.Unmarshal(api(t, "GET", endpoint, other.Token, nil, 200), &list)
	if len(list.Items) != 0 {
		t.Fatal("foreign private rooms leaked")
	}
	recoverURL := endpoint + "/" + host.RoomID + "/session"
	api(t, "POST", recoverURL, other.Token, map[string]any{}, 404)
	var recovered postgres.Credentials
	json.Unmarshal(api(t, "POST", recoverURL, owner.Token, map[string]any{}, 200), &recovered)
	if recovered.ParticipantID != host.ParticipantID || recovered.Token == host.Token {
		t.Fatal("did not restore original participant with fresh token")
	}
	if id, err := app.store.Authenticate(ctx, host.RoomID, recovered.Token); err != nil || id != host.ParticipantID {
		t.Fatal(id, err)
	}
	if _, err := app.store.Authenticate(ctx, host.RoomID, host.Token); game.Code(err) != "unauthorized" {
		t.Fatal("old token not rotated", err)
	}
	rm, err := app.getRoom(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rm.attach(ctx, &client{id: host.ParticipantID, token: host.Token, ctx: ctx}); game.Code(err) != "unauthorized" {
		t.Fatal("stale handshake attached", err)
	}
	guest, err := app.store.Join(ctx, host.RoomID, host.InviteToken, "Next host", game.Spectator)
	if err != nil {
		t.Fatal(err)
	}
	// Host transfer is available while a game is paused.
	if _, err := app.store.Pool().Exec(ctx, `UPDATE rooms SET snapshot=jsonb_set(snapshot,'{phase}','"paused"') WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	state, _ := app.store.Load(ctx, host.RoomID)
	data, _ := json.Marshal(game.TransferPayload{TargetID: guest.ParticipantID})
	out, err := app.store.Execute(ctx, host.RoomID, host.ParticipantID, game.Command{Version: 2, ID: "transfer", Type: "transfer_host", ExpectedSeq: &state.Seq, Data: data})
	if err != nil || out.Code != "ok" {
		t.Fatal(out.Code, err)
	}
	json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &list)
	if list.Items[0].Role == game.Host {
		t.Fatal("creator still shown as host after transfer")
	}
	json.Unmarshal(api(t, "POST", recoverURL, owner.Token, map[string]any{}, 200), &recovered)
	state, _ = app.store.Load(ctx, host.RoomID)
	p, _ := state.ActiveParticipant(recovered.ParticipantID)
	if p.Role == game.Host {
		t.Fatal("recovery stole host role")
	}
	data, _ = json.Marshal(game.RemovalPayload{TargetID: host.ParticipantID, Mode: "kick"})
	out, err = app.store.Execute(ctx, host.RoomID, guest.ParticipantID, game.Command{Version: 2, ID: "remove", Type: "remove_participant", ExpectedSeq: &state.Seq, Data: data})
	if err != nil || out.Code != "ok" {
		t.Fatal(out.Code, err)
	}
	api(t, "POST", recoverURL, owner.Token, map[string]any{}, 403)
	json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &list)
	if list.Items[0].CanReturn {
		t.Fatal("removed creator can return")
	}
}

func TestMyRoomsFinishedExpiryAndAPITokens(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "rooms_history", "long-enough-password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host"}, 201), &host)
	endpoint := h.URL + "/api/my-rooms"
	var pat struct {
		Token string `json:"token"`
	}
	json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "Tool", "scopes": []string{"drafts:read"}}, 201), &pat)
	api(t, "GET", endpoint, pat.Token, nil, 403)
	api(t, "POST", endpoint+"/"+host.RoomID+"/session", pat.Token, map[string]any{}, 403)
	api(t, "GET", endpoint+"?offset=-1", owner.Token, nil, 400)
	if _, err = app.store.Pool().Exec(ctx, `UPDATE rooms SET snapshot=jsonb_set(snapshot,'{phase}','"finished"'),finished_at=now() WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	var list postgres.MyRooms
	json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &list)
	if len(list.Items) != 1 || list.Items[0].Phase != game.Finished {
		t.Fatal("finished room missing", list)
	}
	api(t, "POST", endpoint+"/"+host.RoomID+"/session", owner.Token, map[string]any{}, 200)
	if _, err = app.store.Pool().Exec(ctx, `UPDATE rooms SET created_at=now()-interval '100 days' WHERE id=$1`, host.RoomID); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &list)
	if len(list.Items) != 0 {
		t.Fatal("expired room listed")
	}
	api(t, "POST", endpoint+"/"+host.RoomID+"/session", owner.Token, map[string]any{}, 404)
	api(t, "POST", h.URL+"/api/account/logout", owner.Token, map[string]any{}, 200)
	api(t, "GET", endpoint, owner.Token, nil, 401)
}
