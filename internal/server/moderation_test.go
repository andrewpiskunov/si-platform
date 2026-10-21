package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
)

func TestModerationHistoryAndResolutionHTTP(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	register := func(name string) accounts.Session {
		t.Helper()
		s, e := auth.Register(ctx, name, "long-enough-password", name)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	owner, other, mod := register("mod_owner"), register("reporter"), register("mod_staff")
	ma, e := auth.Authenticate(ctx, mod.Token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, "UPDATE account_users SET role='moderator' WHERE id=$1", ma.User.ID); e != nil {
		t.Fatal(e)
	}
	var d library.Draft
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201), &d); e != nil {
		t.Fatal(e)
	}
	draft := h.URL + "/api/drafts/" + d.Card.ID
	rights := library.Rights{License: "Own", SourceURL: "original", Attribution: "Author", Confirmed: true}
	api(t, "POST", draft+"/publish", owner.Token, map[string]any{"revision": 1, "rights": rights}, 200)
	decide := h.URL + "/api/moderation/" + d.Card.ID
	api(t, "POST", decide, mod.Token, map[string]any{"revision": 1, "decision": "reject", "reason": "Укажите источники"}, 200)
	history := draft + "/decisions"
	api(t, "GET", history, "", nil, 401)
	api(t, "GET", history, other.Token, nil, 404)
	first := api(t, "GET", history+"?limit=1", owner.Token, nil, 200)
	if !bytes.Contains(first, []byte("Укажите источники")) || bytes.Contains(first, []byte("reporter_id")) || bytes.Contains(first, []byte("moderator_id")) {
		t.Fatalf("invalid author audit: %s", first)
	}
	api(t, "POST", draft+"/publish", owner.Token, map[string]any{"revision": 1, "rights": rights}, 200)
	api(t, "POST", decide, mod.Token, map[string]any{"revision": 1, "decision": "approve", "reason": ""}, 200)
	var page struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Next int64 `json:"next_after_id"`
	}
	if e = json.Unmarshal(api(t, "GET", history+"?limit=1", mod.Token, nil, 200), &page); e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Next != page.Items[0].ID {
		t.Fatal("history cursor", page)
	}
	var next struct {
		Items []json.RawMessage `json:"items"`
	}
	if e = json.Unmarshal(api(t, "GET", fmt.Sprintf("%s?after_id=%d&limit=1", history, page.Next), owner.Token, nil, 200), &next); e != nil || len(next.Items) != 1 {
		t.Fatal(next, e)
	}
	for _, q := range []string{"limit=0", "limit=51", "limit=-1", "limit=x", "limit=1&limit=2", "after_id=0&after_id=1", "after_id=-1", "after_id=x", "after_id=9223372036854775808"} {
		api(t, "GET", history+"?"+q, owner.Token, nil, 400)
	}
	var report struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/catalogue/"+d.Card.ID+"/reports", other.Token, map[string]any{"revision": 1, "reason": "Проверить права"}, 201), &report); e != nil {
		t.Fatal(e)
	}
	reports := h.URL + "/api/reports"
	var authorToken, moderatorToken struct {
		Token string `json:"token"`
	}
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "History", "scopes": []string{"drafts:read"}}, 201), &authorToken); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", history, authorToken.Token, nil, 200)
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", mod.Token, map[string]any{"name": "No moderation", "scopes": []string{"drafts:read", "drafts:write"}}, 201), &moderatorToken); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", reports, moderatorToken.Token, nil, 403)
	api(t, "GET", history, moderatorToken.Token, nil, 404)

	api(t, "GET", reports, owner.Token, nil, 403)
	for _, q := range []string{"status=all", "limit=0", "limit=51", "limit=x", "after=no", "after=" + strings.Repeat("A", 32)} {
		api(t, "GET", reports+"?"+q, mod.Token, nil, 400)
	}
	if !bytes.Contains(api(t, "GET", reports, mod.Token, nil, 200), []byte(report.ID)) {
		t.Fatal("missing open report")
	}
	resolution := reports + "/" + report.ID + "/resolution"
	api(t, "POST", resolution, moderatorToken.Token, map[string]any{"disposition": "dismissed", "reason": "Reviewed"}, 403)
	api(t, "POST", resolution, owner.Token, map[string]any{"disposition": "dismissed", "reason": "Reviewed"}, 403)
	for _, body := range []map[string]any{{"disposition": "block", "reason": "Reason"}, {"disposition": "resolved", "reason": " "}, {"disposition": "resolved", "reason": "OK", "unexpected": true}, {"disposition": "resolved", "reason": "bad\x00reason"}} {
		api(t, "POST", resolution, mod.Token, body, 400)
	}
	// A report remains actionable after the package disappears from publication.
	api(t, "POST", draft+"/withdraw", owner.Token, map[string]any{}, 200)
	accepted := api(t, "POST", resolution, mod.Token, map[string]any{"disposition": "dismissed", "reason": "Оснований не найдено"}, 200)
	repeated := api(t, "POST", resolution, mod.Token, map[string]any{"disposition": "dismissed", "reason": "Оснований не найдено"}, 200)
	if !bytes.Equal(accepted, repeated) {
		t.Fatal("non-idempotent resolution")
	}
	for _, field := range []string{`"resolution":"dismissed"`, `"resolution_reason":"Оснований не найдено"`, `"resolved_by":"` + ma.User.ID + `"`, `"resolved_at":"`} {
		if !bytes.Contains(accepted, []byte(field)) {
			t.Fatalf("missing audit %s: %s", field, accepted)
		}
	}
	api(t, "POST", resolution, mod.Token, map[string]any{"disposition": "resolved", "reason": "Changed"}, 409)
	if bytes.Contains(api(t, "GET", reports, mod.Token, nil, 200), []byte(report.ID)) {
		t.Fatal("closed report in open list")
	}
	if !bytes.Contains(api(t, "GET", reports+"?status=resolved", mod.Token, nil, 200), []byte(report.ID)) {
		t.Fatal("closed report missing")
	}
	// Role changes and session revocation apply even to an accepted retry.
	if _, e = app.store.Pool().Exec(ctx, "UPDATE account_users SET role='author' WHERE id=$1", ma.User.ID); e != nil {
		t.Fatal(e)
	}
	api(t, "POST", resolution, mod.Token, map[string]any{"disposition": "dismissed", "reason": "Оснований не найдено"}, 403)
	if _, e = app.store.Pool().Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", ma.SessionID); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", reports, mod.Token, nil, 401)
	req, _ := http.NewRequest("GET", history, nil)
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private audit cacheable")
	}
}
