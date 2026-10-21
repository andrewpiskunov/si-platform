package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"siplatform/examples"
	"siplatform/internal/library"
)

func TestUsageHTTPSessionIsolationAndFreshness(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, e := auth.Register(ctx, "usage_author", "long-enough-password", "Author")
	if e != nil {
		t.Fatal(e)
	}
	other, e := auth.Register(ctx, "usage_other", "long-enough-password", "Other")
	if e != nil {
		t.Fatal(e)
	}
	endpoint := h.URL + "/api/usage"
	api(t, "GET", endpoint, "", nil, 401)
	var u library.Usage
	if e = json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &u); e != nil {
		t.Fatal(e)
	}
	if u.Packs.Used != 0 || u.Storage.LimitBytes != 10<<30 || u.Limits.PackRevisions != 500 {
		t.Fatal("initial usage", u)
	}
	api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201)
	if e = json.Unmarshal(api(t, "GET", endpoint, owner.Token, nil, 200), &u); e != nil || u.Packs.Used != 1 || u.Packs.Remaining != 199 {
		t.Fatal("usage stale", u, e)
	}
	actor, e := auth.Authenticate(ctx, owner.Token)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(api(t, "GET", endpoint+"?user_id="+actor.User.ID, other.Token, nil, 200), &u); e != nil || u.Packs.Used != 0 {
		t.Fatal("cross-user usage", u, e)
	}
	var token struct {
		Token string `json:"token"`
	}
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "No quota grant", "scopes": []string{"drafts:read"}}, 201), &token); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", endpoint, token.Token, nil, 403)
	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private usage cache policy")
	}
	if _, e = app.store.Pool().Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", actor.SessionID); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", endpoint, owner.Token, nil, 401)
}
