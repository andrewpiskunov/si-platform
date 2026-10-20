package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, s *Service, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, "https://quiz.example"+path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}
func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("HTTP status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}
func apiToken(t *testing.T, s *Service, session Session, scopes []string) (string, string) {
	t.Helper()
	rec := call(t, s, "POST", "/tokens", session.Token, map[string]any{"name": "Editor", "scopes": scopes})
	checkStatus(t, rec, 201)
	var out struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.ID == "" {
		t.Fatal("token creation missing credentials")
	}
	return out.Token, out.ID
}

// Catches missing secure cookie flags, session response shape, and cookie removal/revocation.
func TestHTTPCookiesAndLogout(t *testing.T) {
	s, _ := setup(t)
	rec := call(t, s, "POST", "/register", "", map[string]string{"username": "webuser", "password": testPassword, "display_name": "Web"})
	checkStatus(t, rec, 201)
	var session Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil || session.Token == "" {
		t.Fatal("invalid session response", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "si_session" || cookies[0].Value != session.Token || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].MaxAge != 2592000 {
		t.Fatal("unsafe session cookie")
	}
	checkStatus(t, call(t, s, "GET", "/me", session.Token, nil), 200)
	rec = call(t, s, "POST", "/logout", session.Token, nil)
	checkStatus(t, rec, 200)
	if len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("logout did not clear cookie")
	}
	checkStatus(t, call(t, s, "GET", "/me", session.Token, nil), 401)
	login := call(t, s, "POST", "/login", "", map[string]string{"username": "webuser", "password": testPassword})
	checkStatus(t, login, 200)
}

// Catches CSRF, privilege fields, trailing JSON, and unbounded input.
func TestHTTPOriginAndStrictJSON(t *testing.T) {
	s, _ := setup(t)
	cases := []struct {
		body, origin string
		status       int
	}{
		{`{"username":"valid","password":"correct horse battery staple","display_name":"User"}`, "https://evil.example", 403},
		{`{"username":"valid","password":"correct horse battery staple","display_name":"User","role":"admin"}`, "https://quiz.example", 400},
		{`{} {}`, "", 400}, {`{"username":"` + strings.Repeat("a", 17000) + `"}`, "", 400}, {`null`, "", 400},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "https://quiz.example/register", strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		checkStatus(t, rec, c.status)
	}
	req := httptest.NewRequest("POST", "https://quiz.example/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	checkStatus(t, rec, 403)
}

// Catches scope escalation, cross-owner revocation, and token secrets leaking in listings/exports.
func TestAPITokenRestrictionsAndRevocation(t *testing.T) {
	s, pool := setup(t)
	owner, other := register(t, s, "owner"), register(t, s, "other")
	token, id := apiToken(t, s, owner, []string{"drafts:read", "drafts:write"})
	identity, err := s.Authenticate(context.Background(), token)
	if err != nil || !identity.APIToken || identity.User.ID != owner.User.ID || len(identity.Scopes) != 2 {
		t.Fatal("API identity lost scopes", err)
	}
	for _, scopes := range [][]string{{"publish"}, {"moderate"}, {}, {"drafts:read", "drafts:read"}} {
		checkStatus(t, call(t, s, "POST", "/tokens", owner.Token, map[string]any{"name": "Bad", "scopes": scopes}), 400)
	}
	for _, path := range []string{"/me", "/export", "/tokens"} {
		checkStatus(t, call(t, s, "GET", path, token, nil), 403)
	}
	checkStatus(t, call(t, s, "POST", "/tokens", token, map[string]any{"name": "Escalation", "scopes": []string{"ai:generate"}}), 403)
	checkStatus(t, call(t, s, "POST", "/logout", token, nil), 403)
	checkStatus(t, call(t, s, "DELETE", "/tokens/"+id, other.Token, nil), 404)
	for _, path := range []string{"/tokens", "/export"} {
		rec := call(t, s, "GET", path, owner.Token, nil)
		checkStatus(t, rec, 200)
		for _, secret := range []string{token, owner.Token, testPassword, "password_hash", "token_hash"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("account response leaked secret")
			}
		}
	}
	checkStatus(t, call(t, s, "DELETE", "/tokens/"+id, owner.Token, nil), 200)
	if _, err := s.Authenticate(context.Background(), token); err == nil {
		t.Fatal("revoked API token accepted")
	}
	expired, expiredID := apiToken(t, s, owner, []string{"drafts:export"})
	if _, err := pool.Exec(context.Background(), "UPDATE account_credentials SET expires_at=now()-interval '1 second' WHERE id=$1", expiredID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), expired); err == nil {
		t.Fatal("expired API token accepted")
	}
}

// Catches passwords not rechecked, stale credentials surviving password changes, and identity retained after deletion.
func TestPasswordChangeAndDelete(t *testing.T) {
	s, pool := setup(t)
	owner := register(t, s, "change")
	second, err := s.Login(context.Background(), "change", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := apiToken(t, s, owner, []string{"ai:generate"})
	checkStatus(t, call(t, s, "POST", "/password", owner.Token, map[string]string{"current_password": "wrong password", "new_password": "a new long password"}), 401)
	checkStatus(t, call(t, s, "POST", "/password", owner.Token, map[string]string{"current_password": testPassword, "new_password": "a new long password"}), 200)
	for _, revoked := range []string{owner.Token, second.Token, token} {
		if _, err := s.Authenticate(context.Background(), revoked); err == nil {
			t.Fatal("password change left credential active")
		}
	}
	if _, err := s.Login(context.Background(), "change", testPassword); err == nil {
		t.Fatal("old password remains valid")
	}
	replacement, err := s.Login(context.Background(), "change", "a new long password")
	if err != nil {
		t.Fatal(err)
	}
	metadata := call(t, s, "GET", "/tokens", replacement.Token, nil)
	checkStatus(t, metadata, 200)
	if !strings.Contains(metadata.Body.String(), `"name":"Editor"`) {
		t.Fatal("password change erased token metadata")
	}
	checkStatus(t, call(t, s, "DELETE", "/me", replacement.Token, map[string]string{"password": "wrong password"}), 401)
	checkStatus(t, call(t, s, "DELETE", "/me", replacement.Token, map[string]string{"password": "a new long password"}), 200)
	if _, err := s.Authenticate(context.Background(), replacement.Token); err == nil {
		t.Fatal("deleted identity remains active")
	}
	var username, hash *string
	var deleted bool
	if err := pool.QueryRow(context.Background(), "SELECT username,password_hash,deleted_at IS NOT NULL FROM account_users WHERE id=$1", owner.User.ID).Scan(&username, &hash, &deleted); err != nil {
		t.Fatal(err)
	}
	if username != nil || hash != nil || !deleted {
		t.Fatal("deleted identity not anonymized")
	}
	var audit string
	if err := pool.QueryRow(context.Background(), "SELECT string_agg(action, ',') FROM account_audit WHERE user_id=$1", owner.User.ID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit, "password_changed") || !strings.Contains(audit, "deleted") {
		t.Fatal("account changes missing audit")
	}
}

// Catches admission limits missing before expensive authentication work.
func TestHTTPAuthenticationRateLimit(t *testing.T) {
	s, _ := setup(t)
	limited := false
	for i := 0; i < 30; i++ {
		rec := call(t, s, "POST", "/register", "", map[string]string{"username": fmt.Sprintf("u%d", i)})
		if rec.Code == 429 {
			limited = true
			break
		}
		if rec.Code != 400 {
			t.Fatalf("unexpected status %d", rec.Code)
		}
	}
	if !limited {
		t.Fatal("authentication requests not rate limited")
	}
}
