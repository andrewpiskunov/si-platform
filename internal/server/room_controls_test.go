package server

import (
	"context"
	"encoding/json"
	"siplatform/internal/storage/postgres"
	"testing"
)

func TestInviteAndJournalHTTP(t *testing.T) {
	_, h, auth := platform(t)
	owner, err := auth.Register(context.Background(), "controls_author", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host"}, 201), &host)
	p := join(t, h.URL, host, "Player")
	path := h.URL + "/api/rooms/" + host.RoomID
	api(t, "POST", path+"/invite", p.Token, map[string]any{}, 403)
	var invite struct {
		Token string `json:"invite_token"`
	}
	json.Unmarshal(api(t, "POST", path+"/invite", host.Token, map[string]any{}, 200), &invite)
	if len(invite.Token) != 64 || invite.Token == host.InviteToken {
		t.Fatal("invitation unchanged")
	}
	api(t, "POST", path+"/participants", "", map[string]any{"name": "Old", "role": "spectator", "invite_token": host.InviteToken}, 401)
	api(t, "POST", path+"/participants", "", map[string]any{"name": "New", "role": "spectator", "invite_token": invite.Token}, 201)
	api(t, "GET", path+"/results", p.Token, nil, 200)
	api(t, "GET", path+"/results", "", nil, 401)
	api(t, "GET", path+"/results?after_seq=-1", host.Token, nil, 400)
}
