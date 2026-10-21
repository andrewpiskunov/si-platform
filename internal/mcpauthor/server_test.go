package mcpauthor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}
func fixture(t *testing.T) (*httptest.Server, *pgxpool.Pool, string) {
	t.Helper()
	p := testutil.Pool(t)
	ctx := context.Background()
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	sum := sha256.Sum256([]byte(token))
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('owner','owner','Owner','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES('pat','owner',$1,'api',ARRAY['drafts:read','drafts:write','drafts:export'],now()+interval '1 day')`, pgx.QueryExecModeSimpleProtocol, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(New(accounts.New(p, accounts.Options{}), library.New(p), "https://app.example"))
	t.Cleanup(h.Close)
	return h, p, token
}
func client(t *testing.T, h *httptest.Server, token, version string) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: tokenTransport{token, h.Client().Transport}, Timeout: 10 * time.Second}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, e := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.URL, HTTPClient: hc}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func result(t *testing.T, s *mcp.ClientSession, name string, args any, target any) {
	t.Helper()
	r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatal(e)
	}
	if r.IsError {
		t.Fatalf("%s: %+v", name, r.Content)
	}
	if len(r.Content) != 1 {
		t.Fatal(r)
	}
	content, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal(r)
	}
	if e = json.Unmarshal([]byte(content.Text), target); e != nil {
		t.Fatal(e)
	}
}
func document() packs.Document {
	return packs.Document{ID: "doc", Name: "Private MCP", Version: "5.0", Rounds: []packs.Round{{ID: "r1", Name: "Round", Themes: []packs.Theme{{ID: "t1", Name: "Theme", Questions: []packs.Question{{ID: "q1", Price: 100, Right: []string{"answer"}, Params: []packs.Parameter{{Name: "question", Type: "content", Items: []packs.Content{{Type: "text", Value: "Question"}}}}}}}}}}}
}
func TestSDKAuthoringFlowAndRevocation(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			h, p, token := fixture(t)
			s := client(t, h, token, version)
			list, e := s.ListTools(context.Background(), nil)
			if e != nil || len(list.Tools) != 5 {
				t.Fatal(list, e)
			}
			names := map[string]bool{}
			for _, tool := range list.Tools {
				names[tool.Name] = true
			}
			for _, name := range []string{"create_draft", "get_draft", "upsert_questions", "validate_draft", "export_siq"} {
				if !names[name] {
					t.Fatal("missing tool", name)
				}
			}
			var card library.Card
			result(t, s, "create_draft", map[string]any{"idempotency_key": "sdk-create-key-001", "document": document()}, &card)
			if card.Status != "draft" || card.PublishedRevision != 0 {
				t.Fatal(card)
			}
			var draft struct {
				Card     library.Card   `json:"card"`
				Document packs.Document `json:"document"`
			}
			result(t, s, "get_draft", map[string]any{"pack_id": card.ID}, &draft)
			q := draft.Document.Rounds[0].Themes[0].Questions[0]
			q.Right = []string{"edited"}
			result(t, s, "upsert_questions", map[string]any{"pack_id": card.ID, "base_revision": 1, "idempotency_key": "sdk-upsert-key-01", "round_id": "r1", "theme_id": "t1", "questions": []packs.Question{q}}, &card)
			if card.Revision != 2 {
				t.Fatal(card)
			}
			var diagnostics map[string]any
			result(t, s, "validate_draft", map[string]any{"pack_id": card.ID}, &diagnostics)
			var download struct {
				Path     string `json:"download_path"`
				Revision int    `json:"revision"`
			}
			result(t, s, "export_siq", map[string]any{"pack_id": card.ID}, &download)
			if download.Path != "/api/drafts/"+card.ID+"/export?revision=2" || download.Revision != 2 {
				t.Fatal(download)
			}
			for _, args := range []map[string]any{{"pack_id": card.ID, "evil": true}, {"pack_id": card.ID, "revision": -1}} {
				r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_draft", Arguments: args})
				if e == nil && !r.IsError {
					t.Fatal("invalid input accepted")
				}
			}
			if _, e = p.Exec(context.Background(), `UPDATE account_credentials SET revoked_at=now() WHERE id='pat'`); e != nil {
				t.Fatal(e)
			}
			if r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_draft", Arguments: map[string]any{"idempotency_key": "sdk-create-key-001", "document": document()}}); e == nil && !r.IsError {
				t.Fatal("revoked duplicate accepted")
			}
		})
	}
}
func TestHTTPBoundary(t *testing.T) {
	h, p, token := fixture(t)
	for _, tc := range []struct {
		name, auth, origin string
		status             int
	}{{"missing", "", "", 401}, {"wrong", "Bearer invalid", "", 401}, {"origin", "Bearer " + token, "https://evil.example", 403}, {"valid", "Bearer " + token, "", 405}} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", h.URL, nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			response, e := h.Client().Do(r)
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatal(response.StatusCode)
			}
			if response.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal("cacheable")
			}
		})
	}
	r, _ := http.NewRequest("POST", h.URL, strings.NewReader(strings.Repeat("x", (2<<20)+1)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	response, e := h.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 413 {
		t.Fatal("oversized body", response.StatusCode)
	}
	if _, e = p.Exec(context.Background(), `UPDATE account_credentials SET kind='session',scopes='{}' WHERE id='pat'`); e != nil {
		t.Fatal(e)
	}
	r, _ = http.NewRequest("GET", h.URL, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	response, e = h.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("session credential accepted", response.StatusCode)
	}
}

func TestToolsDoNotExposeForeignPublishedDraftOrMissingScopes(t *testing.T) {
	h, p, token := fixture(t)
	ctx := context.Background()
	s := client(t, h, token, "2026-07-28")
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('foreign','foreign','Foreign','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('foreign-session','foreign','foreign-hash','session',now()+interval '1 day')`); e != nil {
		t.Fatal(e)
	}
	foreign := accounts.Identity{User: accounts.User{ID: "foreign"}, SessionID: "foreign-session"}
	d, e := library.New(p).Create(ctx, foreign, library.Save{Document: document()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_revisions SET status='published',rights='{"allow_download":true}' WHERE pack_id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"get_draft", "validate_draft", "export_siq"} {
		r, e := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"pack_id": d.Card.ID}})
		if e != nil {
			t.Fatal(e)
		}
		if !r.IsError {
			t.Fatal("foreign public draft exposed", name)
		}
	}
	if _, e = p.Exec(ctx, `UPDATE account_credentials SET scopes=ARRAY['drafts:read'] WHERE id='pat'`); e != nil {
		t.Fatal(e)
	}
	r, e := s.CallTool(ctx, &mcp.CallToolParams{Name: "create_draft", Arguments: map[string]any{"idempotency_key": "read-only-create-1", "document": document()}})
	if e != nil {
		t.Fatal(e)
	}
	if !r.IsError {
		t.Fatal("read-only write accepted")
	}
}
func TestHTTPAdmissionIsBounded(t *testing.T) {
	h, p, token := fixture(t)
	ctx := context.Background()
	s := client(t, h, token, "2026-07-28")
	var card library.Card
	result(t, s, "create_draft", map[string]any{"idempotency_key": "admission-create-1", "document": document()}, &card)
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id='owner' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 16)
	for range 16 {
		go func() {
			r, e := s.CallTool(ctx, &mcp.CallToolParams{Name: "get_draft", Arguments: map[string]any{"pack_id": card.ID}})
			if e == nil && r.IsError {
				e = fmt.Errorf("unexpected tool error")
			}
			done <- e
		}()
	}
	// Real HTTP requests are held behind the account lock/pool, not synthetic slots.
	end := time.Now().Add(3 * time.Second)
	for len(h.Config.Handler.(*Server).slots) != 16 {
		if time.Now().After(end) {
			t.Fatal("requests did not enter admission")
		}
		time.Sleep(time.Millisecond)
	}

	r, _ := http.NewRequest("GET", h.URL, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	response, e := h.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 429 {
		t.Fatal("unbounded admission", response.StatusCode)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	for range 16 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
}

func TestDeepJSONRejectedBeforeSchemaWork(t *testing.T) {
	h, _, token := fixture(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"get_draft","arguments":{"pack_id":"` + strings.Repeat("a", 32) + `","nested":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}}}`
	r, _ := http.NewRequest("POST", h.URL, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	response, e := h.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("deep body reached schema validation", response.StatusCode)
	}
}

func TestLatestRequestCancellationReleasesAdmission(t *testing.T) {
	h, p, token := fixture(t)
	ctx := context.Background()
	s := client(t, h, token, "2026-07-28")
	var card library.Card
	result(t, s, "create_draft", map[string]any{"idempotency_key": "cancel-create-0001", "document": document()}, &card)
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id='owner' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, e = s.CallTool(bounded, &mcp.CallToolParams{Name: "get_draft", Arguments: map[string]any{"pack_id": card.ID}}); e == nil {
		t.Fatal("cancelled call succeeded")
	}
	end := time.Now().Add(2 * time.Second)
	for len(h.Config.Handler.(*Server).slots) > 0 {
		if time.Now().After(end) {
			t.Fatal("cancelled request retained slot")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSuccessfulDraftResponseIsNotStored(t *testing.T) {
	h, _, token := fixture(t)
	s := client(t, h, token, "2026-07-28")
	var card library.Card
	result(t, s, "create_draft", map[string]any{"idempotency_key": "cache-regression-01", "document": document()}, &card)
	req, _ := http.NewRequest("POST", h.URL, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"get_draft","arguments":{"pack_id":%q}}}`, card.ID)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "get_draft")
	resp, err := h.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatal(resp.Status, string(b))
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache header = %q", got)
	}
}

func TestJSONOnlyTransportRejectsSubscriptions(t *testing.T) {
	h, _, token := fixture(t)
	s := client(t, h, token, "2026-07-28")
	if caps := s.InitializeResult().Capabilities; caps.Tools == nil || caps.Tools.ListChanged {
		t.Errorf("unexpected tools capabilities: %+v", caps.Tools)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", h.URL, strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"subscriptions/listen","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"notifications":{"toolsListChanged":true}}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	resp, err := h.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("subscription accepted: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
}
