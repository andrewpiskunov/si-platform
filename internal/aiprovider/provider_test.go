package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"siplatform/internal/aiformat"
	"strings"
	"testing"
)

func req() aiformat.Request {
	return aiformat.Request{Mode: "theme", Topic: "Science", Audience: "Adults", Difficulty: 2, Themes: 1, QuestionsPerTheme: 1}
}

const valid = `{"title":"Quiz","themes":[{"name":"Science","questions":[{"text":"What?","answer":"Yes","comment":"","sources":[]}]}]}`

func response(status, text string, usage any) map[string]any {
	return map[string]any{"status": status, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "usage": usage}
}
func TestPayloadAndUsage(t *testing.T) {
	var bodies []map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			json.NewEncoder(w).Encode(map[string]any{"input_tokens": 123})
			return
		}
		json.NewEncoder(w).Encode(response("completed", valid, map[string]any{"input_tokens": 123, "output_tokens": 40}))
	}))
	defer s.Close()
	p := newClient("secret", s.URL, s.Client())
	n, e := p.Count(context.Background(), req(), "test", 256)
	if e != nil || n != 123 {
		t.Fatalf("count %d %v", n, e)
	}
	v, u, e := p.Generate(context.Background(), req(), "test", 256)
	if e != nil || !u.Known || u.OutputTokens != 40 || v.Title != "Quiz" {
		t.Fatalf("generate %v %v %v", v, u, e)
	}
	if !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("count differs from generate: %v", bodies)
	}
	b := bodies[0]
	if b["store"] != false || b["tools"] != nil || b["text"].(map[string]any)["format"].(map[string]any)["strict"] != true {
		t.Fatal("unsafe payload")
	}
}
func TestFailuresAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  any
		want  error
		known bool
	}{{"incomplete", response("incomplete", valid, map[string]any{"input_tokens": 1, "output_tokens": 2}), ErrIncomplete, true}, {"invalid", response("completed", `{"title":"x"}`, nil), ErrInvalidOutput, false}, {"extra", response("completed", strings.Replace(valid, `"title":`, `"evil":true,"title":`, 1), nil), ErrInvalidOutput, false}, {"refusal", map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "refusal", "refusal": "secret"}}}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 2}}, ErrRefused, true}, {"badusage", response("completed", valid, map[string]any{"input_tokens": -1, "output_tokens": 2}), nil, false}, {"overusage", response("completed", valid, map[string]any{"input_tokens": 1, "output_tokens": 257}), nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(tc.body) }))
			defer s.Close()
			_, u, e := newClient("secret", s.URL, s.Client()).Generate(context.Background(), req(), "test", 256)
			if e != tc.want || u.Known != tc.known {
				t.Fatalf("got %v %v", u, e)
			}
		})
	}
}
func TestTransportBoundaries(t *testing.T) {
	for _, kind := range []string{"error", "oversize", "redirect", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch kind {
				case "error":
					http.Error(w, "SECRET", 500)
				case "oversize":
					w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
				case "redirect":
					http.Redirect(w, r, "/again", 302)
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			_, u, e := newClient("secret", s.URL, s.Client()).Generate(ctx, req(), "test", 256)
			if e != ErrUnavailable || u.Known || calls > 1 {
				t.Fatalf("unsafe failure %v %v calls %d", u, e, calls)
			}
		})
	}
}
func TestFixture(t *testing.T) {
	p := NewFixture()
	r := req()
	r.Mode = "pack"
	r.Themes = 10
	r.QuestionsPerTheme = 10
	a, u, e := p.Generate(context.Background(), r, "fixture", 16384)
	if e != nil || !u.Known || aiformat.ValidateResult(r, a) != nil {
		t.Fatalf("fixture %v %v", u, e)
	}
	b, _, _ := p.Generate(context.Background(), r, "fixture", 16384)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("nondeterministic")
	}
}
func TestRequiredComment(t *testing.T) {
	for _, body := range []string{strings.Replace(valid, `"comment":"",`, "", 1), strings.Replace(valid, `"comment":""`, `"comment":null`, 1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(response("completed", body, nil))
		}))
		_, _, err := newClient("", s.URL, s.Client()).Generate(context.Background(), req(), "test", 256)
		s.Close()
		if err != ErrInvalidOutput {
			t.Fatalf("missing/null field accepted: %v", err)
		}
	}
}
