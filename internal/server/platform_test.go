package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

func platform(t *testing.T) (*Server, *httptest.Server, *accounts.Service) {
	t.Helper()
	pool := testutil.Pool(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	objects, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := accounts.New(pool, accounts.Options{Origin: "http://trusted.example"})
	app := New(postgres.New(pool), Options{Origin: "http://trusted.example", Accounts: auth, Library: library.New(pool), Objects: objects})
	server := httptest.NewServer(app.Handler())
	t.Cleanup(func() { app.Close(); server.Close() })
	return app, server, auth
}
func api(t *testing.T, method, url, token string, body any, want int) []byte {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d want %d: %s", method, url, resp.StatusCode, want, b)
	}
	return b
}
func TestAuthoringRoomBoundary(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "owner", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "other", "long-enough-password", "Другой")
	if err != nil {
		t.Fatal(err)
	}
	api(t, "POST", h.URL+"/api/rooms", "", map[string]any{"host_name": "Host"}, 401)
	var draft library.Draft
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201), &draft); err != nil {
		t.Fatal(err)
	}
	path := h.URL + "/api/drafts/" + draft.Card.ID
	api(t, "GET", path, other.Token, nil, 404)
	doc := draft.Document
	doc.Name = "Моя новая версия"
	var newer library.Draft
	json.Unmarshal(api(t, "PUT", path, owner.Token, map[string]any{"base_revision": draft.Card.Revision, "document": doc}, 200), &newer)
	api(t, "PUT", path, owner.Token, map[string]any{"base_revision": draft.Card.Revision, "document": doc}, 409)
	if newer.Card.Revision != draft.Card.Revision+1 {
		t.Fatal("revision not incremented")
	}
	var host postgres.Credentials
	json.Unmarshal(api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "pack_id": draft.Card.ID, "revision": newer.Card.Revision, "title": "Вечер игры", "theme": "pirate"}, 201), &host)
	p := join(t, h.URL, host, "Player")
	join(t, h.URL, host, "Other")
	board := api(t, "GET", h.URL+"/api/rooms/"+host.RoomID+"/board", p.Token, nil, 200)
	if bytes.Contains(board, []byte(doc.Rounds[0].Themes[0].Questions[0].Right[0])) || bytes.Contains(board, []byte("document")) {
		t.Fatalf("board leaks content: %s", board)
	}
	hws, initial := connect(t, h.URL, host)
	pws, _ := connect(t, h.URL, p)
	question := doc.Rounds[0].Themes[0].Questions[0]
	selectURL := h.URL + "/api/rooms/" + host.RoomID + "/select"
	api(t, "POST", h.URL+"/api/rooms/"+host.RoomID+"/round", host.Token, map[string]any{"round_id": doc.Rounds[0].ID, "command_id": "round1", "expected_seq": initial.State.Seq}, 200)
	current, _ := app.store.Load(ctx, host.RoomID)
	selection := map[string]any{"question_id": question.ID, "command_id": "select1", "expected_seq": current.Seq}
	api(t, "POST", selectURL, other.Token, selection, 401)
	api(t, "POST", selectURL, host.Token, selection, 200)
	api(t, "POST", selectURL, host.Token, selection, 200) // transport retry must remain idempotent despite old seq
	revealed := readUntil(t, pws, func(m Message) bool {
		return m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Question != nil
	})
	if revealed.State.Gameplay.Question.Answer != nil {
		t.Fatal("answer leaked")
	}
	write(t, hws, game.Command{ID: "bypass", Type: "open_question", QuestionID: "invented", Text: "injected", Answer: "injected", Value: 100})
	if got := result(t, hws, "bypass"); got.Code != "forbidden" {
		t.Fatalf("raw question bypass: %+v", got)
	}
	stage := revealed.State.Gameplay.Stage
	open := sendV2(t, app, hws, host, "open1", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	stage = open.State.Gameplay.Stage
	buzz := sendV2(t, app, pws, p, "buzz1", "buzz", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	stage = buzz.State.Gameplay.Stage
	answered := sendV2(t, app, pws, p, "answer1", "answer", game.AnswerPayload{StageID: stage.ID, InstanceID: stage.InstanceID, Text: question.Right[0]})
	stage = answered.State.Gameplay.Stage
	correct := true
	sendV2(t, app, hws, host, "judge1", "judge", game.JudgePayload{StageID: stage.ID, InstanceID: stage.InstanceID, TargetID: p.ParticipantID, Correct: &correct})
	// The room stays pinned to the selected version after author changes the draft.
	doc.Rounds[0].Themes[0].Questions[1].Params[0].Items[0].Value = "CHANGED AFTER PIN"
	api(t, "PUT", path, owner.Token, map[string]any{"base_revision": newer.Card.Revision, "document": doc}, 200)
	state, err := app.store.Load(ctx, host.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	selection = map[string]any{"question_id": doc.Rounds[0].Themes[0].Questions[1].ID, "command_id": "select2", "expected_seq": state.Seq}
	api(t, "POST", selectURL, host.Token, selection, 200)
	m := readUntil(t, pws, func(m Message) bool {
		return m.State != nil && m.State.Gameplay != nil && m.State.Gameplay.Question != nil && m.State.Gameplay.Question.ID == doc.Rounds[0].Themes[0].Questions[1].ID
	})
	if strings.Contains(m.State.Gameplay.Question.Text, "CHANGED") {
		t.Fatal("room switched to mutable revision")
	}
}
func TestAccountMountAndStrictOrigin(t *testing.T) {
	_, h, _ := platform(t)
	data := api(t, "POST", h.URL+"/api/account/register", "", map[string]string{"username": "member", "password": "long-enough-password", "display_name": "Member"}, 201)
	var session accounts.Session
	json.Unmarshal(data, &session)
	api(t, "GET", h.URL+"/api/account/me", session.Token, nil, 200)
	req, _ := http.NewRequest("POST", h.URL+"/api/drafts", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-site mutation accepted")
	}
}

func TestPublicationDownloadsAndModerationRevocation(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "owner", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := auth.Register(ctx, "moderator", "long-enough-password", "Модератор")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.Pool().Exec(ctx, "UPDATE account_users SET role='moderator' WHERE id=$1", mod.User.ID); err != nil {
		t.Fatal(err)
	}
	var d library.Draft
	json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201), &d)
	path := h.URL + "/api/drafts/" + d.Card.ID
	public := h.URL + "/api/catalogue/" + d.Card.ID
	api(t, "POST", path+"/publish", owner.Token, map[string]any{"revision": 1, "rights": library.Rights{License: "CC0", SourceURL: "original", Attribution: "Автор", Confirmed: true, AllowDownload: true}}, 200)
	api(t, "GET", public, "", nil, 404)
	api(t, "POST", h.URL+"/api/moderation/"+d.Card.ID, owner.Token, map[string]any{"revision": 1, "decision": "approve", "reason": "ok"}, 403)
	api(t, "POST", h.URL+"/api/moderation/"+d.Card.ID, mod.Token, map[string]any{"revision": 1, "decision": "approve", "reason": "Проверено"}, 200)
	card := api(t, "GET", public, "", nil, 200)
	if bytes.Contains(card, []byte(`"right":`)) || bytes.Contains(card, []byte(`"56"`)) {
		t.Fatal("public card leaks answer")
	}
	archive := api(t, "GET", public+"/download", "", nil, 200)
	if !bytes.HasPrefix(archive, []byte("PK")) {
		t.Fatal("not SIQ")
	}
	api(t, "POST", public+"/reports", owner.Token, map[string]any{"revision": 1, "reason": "Проверка жалобы"}, 201)
	api(t, "POST", h.URL+"/api/moderation/"+d.Card.ID, mod.Token, map[string]any{"revision": 1, "decision": "block", "reason": "Проверка блокировки"}, 200)
	api(t, "GET", public+"/download", "", nil, 403)
	api(t, "POST", h.URL+"/api/rooms", owner.Token, map[string]any{"host_name": "Host", "pack_id": d.Card.ID, "revision": 1}, 403)
}
