package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"siplatform/internal/ai"
	"siplatform/internal/aiformat"
	"siplatform/internal/aiprovider"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"strings"
	"testing"
	"time"
)

func TestAIAuthoringHTTPPrivateReviewFlow(t *testing.T) {
	app, h, auth := platform(t)
	service, err := ai.New(app.store.Pool(), ai.Options{Provider: aiprovider.NewFixture(), ProviderLabel: "fixture", Model: "fixture-v1", InputRate: 1000000, OutputRate: 1000000, MonthlyLimit: 10000000, MaxOutputTokens: 256})
	if err != nil {
		t.Fatal(err)
	}
	app.options.AI = service
	ctx := context.Background()
	owner, err := auth.Register(ctx, "ai_http_author", "long-enough-password", "AI author")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "ai_http_other", "long-enough-password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	api(t, "GET", h.URL+"/api/ai/status", "", nil, 401)
	body := api(t, "GET", h.URL+"/api/ai/status", owner.Token, nil, 200)
	if !bytes.Contains(body, []byte(`"provider":"fixture"`)) {
		t.Fatal("fixture not marked")
	}
	api(t, "GET", h.URL+"/api/ai/jobs?after=a&after=b", owner.Token, nil, 400)
	input := aiformat.Request{Mode: "pack", Topic: "private-author-topic", Audience: "Друзья", Difficulty: 2, Themes: 1, QuestionsPerTheme: 2}
	var quote ai.Quote
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/ai/quotes", owner.Token, input, 201), &quote); err != nil {
		t.Fatal(err)
	}
	var job ai.Job
	start := map[string]string{"quote_id": quote.ID, "idempotency_key": "http-test-request"}
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/ai/jobs", owner.Token, start, 201), &job); err != nil {
		t.Fatal(err)
	}
	api(t, "GET", h.URL+"/api/ai/jobs/"+job.ID, other.Token, nil, 404)
	api(t, "POST", h.URL+"/api/ai/jobs/"+job.ID+"/cancel", other.Token, map[string]any{}, 404)
	stop, err := service.StartWorkers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err = json.Unmarshal(api(t, "GET", h.URL+"/api/ai/jobs/"+job.ID, owner.Token, nil, 200), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "ready" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if job.Status != "ready" || job.Result == nil {
		t.Fatalf("job not ready: %+v", job)
	}
	edited := *job.Result
	edited.Themes[0].Questions[0].Text = "Исправленный автором вопрос?"
	previewRequest := map[string]any{"reviewed": true, "result": edited}
	var preview struct {
		Document packs.Document `json:"document"`
	}
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/ai/jobs/"+job.ID+"/preview", owner.Token, previewRequest, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Document.Rounds[0].Themes[0].Questions[0].Content("question")[0].Value != "Исправленный автором вопрос?" {
		t.Fatal("edited result ignored")
	}
	api(t, "POST", h.URL+"/api/ai/jobs/"+job.ID+"/draft", owner.Token, map[string]any{"reviewed": false, "result": edited}, 400)
	var draft library.Draft
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/ai/jobs/"+job.ID+"/draft", owner.Token, previewRequest, 200), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Card.Status != "draft" || draft.Card.PublishedRevision != 0 {
		t.Fatal("AI published without author publication")
	}
	var repeated library.Draft
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/ai/jobs/"+job.ID+"/draft", owner.Token, previewRequest, 200), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Card.ID != draft.Card.ID {
		t.Fatal("duplicate private pack")
	}
	// Whole-account ZIP exports own prompts/results/costs, never credential IDs.
	response, archive := privacyRequest(t, h.URL+"/api/privacy/export?metadata_only=true", owner.Token, "")
	if response.StatusCode != 200 {
		t.Fatalf("export %d", response.StatusCode)
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var jobData string
	for _, entry := range z.File {
		if strings.Contains(entry.Name, "ai_jobs") {
			r, e := entry.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			jobData = string(b)
		}
	}
	if !strings.Contains(jobData, input.Topic) || !strings.Contains(jobData, job.ID) {
		t.Fatal("own AI data absent from account export")
	}
	actor, err := auth.Authenticate(ctx, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jobData, actor.SessionID) || strings.Contains(jobData, owner.Token) || strings.Contains(jobData, "credential_id") {
		t.Fatal("exported AI credential")
	}
	req, _ := http.NewRequest("POST", h.URL+"/api/ai/jobs/"+job.ID+"/preview", strings.NewReader(`{"reviewed":true,"result":null,"extra":true}`))
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin AI request accepted")
	}
}
