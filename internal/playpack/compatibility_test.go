package playpack

import (
	"siplatform/examples"
	"siplatform/internal/packs"
	"strings"
	"testing"
)

func TestDiagnosticsMatchCommands(t *testing.T) {
	tests := []struct {
		name   string
		change func(*packs.Round, *packs.Question)
		valid  bool
	}{
		{"ordinary", func(*packs.Round, *packs.Question) {}, true},
		{"zero", func(_ *packs.Round, q *packs.Question) { q.Price = 0 }, false},
		{"large_price", func(_ *packs.Round, q *packs.Question) { q.Price = 100001 }, false},
		{"id", func(_ *packs.Round, q *packs.Question) { q.ID = "вопрос" }, false},
		{"long_text", func(_ *packs.Round, q *packs.Question) { q.Params[0].Items[0].Value = strings.Repeat("я", 2049) }, false},
		{"long_answer", func(_ *packs.Round, q *packs.Question) { q.Right = []string{strings.Repeat("a", 4097)} }, false},
		{"final", func(r *packs.Round, _ *packs.Question) { r.Type = "final" }, false},
		{"stake", func(_ *packs.Round, q *packs.Question) { q.Type = "stake" }, false},
		{"media", func(_ *packs.Round, q *packs.Question) { q.Params[0].Items[0].Type = "audio" }, false},
		{"timing", func(_ *packs.Round, q *packs.Question) { q.Params[0].Items[0].DurationMS = 1000 }, false},
		{"replic", func(_ *packs.Round, q *packs.Question) { q.Params[0].Items[0].Placement = "replic" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := examples.Demo()
			doc.Rounds = doc.Rounds[:1] // Legacy command coverage uses the ordinary round only.
			round := &doc.Rounds[0]
			q := &round.Themes[0].Questions[0]
			tc.change(round, q)
			issues := ValidateLegacy(doc)
			cmd, err := Command(*round, *q, "select", 1)
			if packs.Playable(issues) != tc.valid || (err == nil) != tc.valid {
				t.Fatalf("diagnostics/command mismatch: %+v %v", issues, err)
			}
			if tc.valid {
				if err := cmd.Validate(); err != nil {
					t.Fatal(err)
				}
			} else {
				if issues[0].Path == "" || issues[0].Message == "" {
					t.Fatal("non-actionable diagnostic")
				}
			}
		})
	}
}
