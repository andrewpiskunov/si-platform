package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"siplatform/internal/library"
	"siplatform/internal/siq"
	"testing"
)

type mcpTokenTransport struct{ token string }

func (t mcpTokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}
func TestMCPRouteCreatesAndExportsRealSIQ(t *testing.T) {
	_, h, auth := platform(t)
	ctx := context.Background()
	owner, e := auth.Register(ctx, "mcp_server_author", "long-enough-password", "MCP author")
	if e != nil {
		t.Fatal(e)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "SDK test", "scopes": []string{"drafts:read", "drafts:write", "drafts:export"}}, 201), &issued); e != nil {
		t.Fatal(e)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: h.URL + "/api/mcp", HTTPClient: &http.Client{Transport: mcpTokenTransport{issued.Token}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	doc := emptyDocument()
	doc.Name = "MCP SIQ"
	q := &doc.Rounds[0].Themes[0].Questions[0]
	q.Right = []string{"Answer"}
	q.Params[0].Items[0].Value = "Question"
	r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_draft", Arguments: map[string]any{"idempotency_key": "integration-create-1", "document": doc}})
	if e != nil {
		t.Fatal(e)
	}
	if r.IsError || len(r.Content) != 1 {
		t.Fatal(r)
	}
	var card library.Card
	if e = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &card); e != nil {
		t.Fatal(e)
	}
	r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "export_siq", Arguments: map[string]any{"pack_id": card.ID}})
	if e != nil || r.IsError {
		t.Fatal(r, e)
	}
	var download struct {
		Path string `json:"download_path"`
	}
	if e = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &download); e != nil {
		t.Fatal(e)
	}
	body := api(t, "GET", h.URL+download.Path, issued.Token, nil, 200)
	archive, e := siq.Read(ctx, bytes.NewReader(body), int64(len(body)), siq.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	if archive.Document.Name != "MCP SIQ" || archive.Document.Rounds[0].Themes[0].Questions[0].Right[0] != "Answer" {
		t.Fatal("SIQ mismatch")
	}
}
