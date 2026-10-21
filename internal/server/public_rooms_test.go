package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func TestPublicRoomsHTTP(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "public_owner", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	var host, private postgres.Credentials
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "PRIVATE-HOST", "title": "Public evening", "settings": map[string]any{"visibility": "public", "voice_url": "https://voice.example/SECRET-LINK"}}, 201), &host); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Private"}, 201), &private)
	response, err := http.Get(h.URL + "/api/rooms")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("public response", response.StatusCode, string(raw), err)
	}
	var page postgres.PublicRooms
	if err = json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 || page.Items[0].RoomID != host.RoomID {
		t.Fatal("discovery", page, err)
	}
	for _, value := range []string{private.RoomID, "PRIVATE-HOST", "SECRET-LINK", "invite", "token", "participants", "pack_id"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("list leak", value)
		}
	}
	app.mu.Lock()
	actors := len(app.rooms)
	app.mu.Unlock()
	if actors != 0 {
		t.Fatal("listing allocated actors", actors)
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=x", "?after=no", "?after=" + strings.Repeat("A", 32)} {
		api(t, "GET", h.URL+"/api/rooms"+query, "", nil, 400)
	}
	for _, settings := range []any{map[string]any{}, map[string]any{"visibility": "friends"}, map[string]any{"visibility": "public", "voice_url": "javascript:alert(1)"}, map[string]any{"visibility": "public", "voice_url": "https://user:secret@example.com"}, map[string]any{"visibility": "public", "unknown": true}} {
		api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Invalid", "settings": settings}, 400)
	}
	api(t, "POST", h.URL+"/api/rooms/"+private.RoomID+"/participants", "", map[string]any{"name": "Guest", "role": "player"}, 401)
	var player postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/participants", "", map[string]any{"name": "Guest", "role": "player"}, 201), &player)
	hws, initial := connect(t, h.URL, host)
	pws, _ := connect(t, h.URL, player)
	if initial.State.Settings.Visibility != "public" || initial.State.Settings.VoiceURL != "https://voice.example/SECRET-LINK" {
		t.Fatal("missing settings", initial.State.Settings)
	}
	data, _ := json.Marshal(game.SettingsPayload{Settings: game.RoomSettings{Visibility: "private"}})
	cmd := game.Command{Version: 2, ID: "close", Type: "set_room_settings", ExpectedSeq: &initial.State.Seq, Data: data}
	writeSettingsCommand(t, pws, cmd)
	if got := result(t, pws, "close"); got.Code != "forbidden" {
		t.Fatal("player settings", got.Code)
	}
	writeSettingsCommand(t, hws, cmd)
	if got := result(t, hws, "close"); got.Code != "ok" {
		t.Fatal("host settings", got.Code)
	}
	readUntil(t, pws, func(m Message) bool { return m.State != nil && m.State.Settings.Visibility == "private" })
	api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/participants", "", map[string]any{"name": "Stale list", "role": "spectator"}, 401)
	api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/participants", "", map[string]any{"name": "Old link", "role": "spectator", "invite_token": host.InviteToken}, 401)
	json.Unmarshal(api(t, "GET", h.URL+"/api/rooms", "", nil, 200), &page)
	if len(page.Items) != 0 {
		t.Fatal("closed room listed", page)
	}
	api(t, "GET", h.URL+"/api/rooms/"+host.RoomID+"/results", player.Token, nil, 200)
	api(t, "GET", h.URL+"/api/rooms/"+host.RoomID+"/board", "", nil, 401)
}

func TestRoomSettingsFollowTransferredGuestHost(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "transfer_settings", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	var host, guest postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host"}, 201), &host)
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/participants", "", map[string]any{"name": "Guest host", "role": "spectator", "invite_token": host.InviteToken}, 201), &guest)
	hws, _ := connect(t, h.URL, host)
	gws, _ := connect(t, h.URL, guest)
	send := func(c postgres.Credentials, kind, id string, data any) game.Command {
		state, e := app.store.Load(ctx, host.RoomID)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(data)
		return game.Command{Version: 2, ID: id, Type: kind, ExpectedSeq: &state.Seq, Data: raw}
	}
	join(t, h.URL, host, "First player")
	join(t, h.URL, host, "Second player")
	state, _ := app.store.Load(ctx, host.RoomID)
	api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/round", host.Token, map[string]any{"round_id": examples.Demo().Rounds[0].ID, "command_id": "start", "expected_seq": state.Seq}, 200)
	sendV2(t, app, hws, host, "pause-settings", "pause", game.EmptyPayload{})
	writeSettingsCommand(t, hws, send(host, "transfer_host", "transfer", game.TransferPayload{TargetID: guest.ParticipantID}))
	if got := result(t, hws, "transfer"); got.Code != "ok" {
		t.Fatal("transfer", got.Code)
	}
	settings := game.SettingsPayload{Settings: game.RoomSettings{Visibility: "public", VoiceURL: "https://voice.example/room"}}
	writeSettingsCommand(t, hws, send(host, "set_room_settings", "former", settings))
	if got := result(t, hws, "former"); got.Code != "forbidden" {
		t.Fatal("former host", got.Code)
	}
	writeSettingsCommand(t, gws, send(guest, "set_room_settings", "guest-settings", settings))
	if got := result(t, gws, "guest-settings"); got.Code != "ok" {
		t.Fatal("guest host", got.Code)
	}
	var page postgres.PublicRooms
	json.Unmarshal(api(t, "GET", h.URL+"/api/rooms", "", nil, 200), &page)
	if len(page.Items) != 1 || page.Items[0].Phase != game.Paused {
		t.Fatal("paused public room", page)
	}
}

func writeSettingsCommand(t *testing.T, conn *websocket.Conn, command game.Command) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, command); err != nil {
		t.Fatal(err)
	}
}
