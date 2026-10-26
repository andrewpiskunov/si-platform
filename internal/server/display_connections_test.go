package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestDisplayConnectionsAllowHostPreviewsAndExternalScreens(t *testing.T) {
	_, h, _ := setup(t)
	host := create(t, h.URL)
	// The two primary host tabs must remain available independently of previews.
	for i := 0; i < 2; i++ {
		_, m := connect(t, h.URL, host)
		if m.Type != "snapshot" {
			t.Fatalf("primary %d: %+v", i, m)
		}
	}
	authenticate := func(query string) Message {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.URL, "http")+"/api/rooms/"+host.RoomID+"/ws?"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ws.CloseNow() })
		if err := wsjson.Write(ctx, ws, map[string]any{"v": 1, "type": "authenticate", "token": host.Token}); err != nil {
			t.Fatal(err)
		}
		var m Message
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for i := 0; i < 4; i++ {
		m := authenticate("display=1")
		if m.Type != "snapshot" || m.State == nil || m.State.You != "" {
			t.Fatalf("spectator projection %d: %+v", i, m)
		}
	}
	if m := authenticate("display=1"); m.Type != "error" || m.Code != "connection_limit" {
		t.Fatalf("fifth display must remain bounded: %+v", m)
	}
	_, m := connect(t, h.URL, host)
	if m.Type != "error" || m.Code != "connection_limit" {
		t.Fatalf("primary limit changed: %+v", m)
	}
	for i := 0; i < 2; i++ {
		if m := authenticate("appearance_only=1"); m.Type != "appearance_refresh" {
			t.Fatalf("appearance %d: %+v", i, m)
		}
	}
	if m := authenticate("appearance_only=1"); m.Type != "error" || m.Code != "connection_limit" {
		t.Fatalf("appearance limit changed: %+v", m)
	}
}
