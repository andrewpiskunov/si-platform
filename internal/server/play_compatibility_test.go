package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
)

func TestCreateGameRejectsIncompatibleSIQ(t *testing.T) {
	_, h, auth := platform(t)
	owner, err := auth.Register(context.Background(), "compat_author", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*packs.Document)
	}{
		{"large_price", func(d *packs.Document) { d.Rounds[0].Themes[0].Questions[0].Price = 100001 }},
		{"question_id", func(d *packs.Document) { d.Rounds[0].Themes[0].Questions[0].ID = "вопрос" }},
		{"text_length", func(d *packs.Document) {
			d.Rounds[0].Themes[0].Questions[0].Params[0].Items[0].Value = strings.Repeat("т", 2049)
		}},
		{"final_round", func(d *packs.Document) { d.Rounds[0].Type = "final" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := examples.Demo()
			tc.edit(&doc)
			var d library.Draft
			if err := json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": doc}, 201), &d); err != nil {
				t.Fatal(err)
			}
			if !packs.Playable(d.Issues) {
				t.Fatalf("fixture should remain valid SIQ: %+v", d.Issues)
			}
			// SIQ authoring/export remains available independently of current engine support.
			api(t, "GET", h.URL+"/api/drafts/"+d.Card.ID+"/export", owner.Token, nil, 200)
			api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "pack_id": d.Card.ID}, 400)
		})
	}
}

func TestPinnedFinalCannotBecomeOrdinaryQuestion(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "final_author", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(ctx, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	doc := examples.Demo()
	doc.Rounds[0].Type = "final"
	// A previously pinned room still needs an independent selection guard.
	host, err := app.store.CreateGame(ctx, "Host", postgres.GameSetup{ActorID: actor.User.ID, CredentialID: actor.SessionID, Title: "Final", Theme: "classic", DemoDocument: &doc})
	if err != nil {
		t.Fatal(err)
	}
	join(t, h.URL, host, "One")
	join(t, h.URL, host, "Two")
	_, initial := connect(t, h.URL, host)
	response := api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/select", host.Token, map[string]any{"question_id": doc.Rounds[0].Themes[0].Questions[0].ID, "command_id": "final", "expected_seq": initial.State.Seq}, 400)
	if !strings.Contains(string(response), "unsupported_question") {
		t.Fatalf("missing diagnostic: %s", response)
	}
	state, err := app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != game.Lobby || state.Question != nil {
		t.Fatal("final silently converted to ordinary question")
	}
}
