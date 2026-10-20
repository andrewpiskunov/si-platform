package playpack

import (
	"fmt"
	"siplatform/examples"
	"siplatform/internal/packs"
	"strings"
	"testing"
)

func TestV2CompilerTypesAndFinalDefault(t *testing.T) {
	for _, kind := range []string{"simple", "withButton", "stake", "stakeAll", "secret", "forAll", "noRisk"} {
		t.Run(kind, func(t *testing.T) {
			d := examples.Demo()
			d.Rounds[0].Themes[0].Questions[0].Type = kind
			q, e := Compile(d, d.Rounds[0].Themes[0].Questions[0].ID)
			if e != nil || q.Type == "" {
				t.Fatalf("%+v %v", q, e)
			}
		})
	}
	d := examples.Demo()
	d.Rounds[0].Type = "final"
	d.Rounds[0].Themes[0].Questions[0].Type = ""
	q, e := Compile(d, d.Rounds[0].Themes[0].Questions[0].ID)
	if e != nil || q.Type != "stakeAll" {
		t.Fatalf("final default: %+v %v", q, e)
	}
}
func TestV2CompilerZeroStepsReplicAndDuration(t *testing.T) {
	d := examples.Demo()
	p := &d.Rounds[0].Themes[0].Questions[0]
	p.Price = 0
	p.Params[0].Items = append(p.Params[0].Items, packs.Content{Type: "text", Value: "Host instruction", Placement: "replic", DurationMS: 1234, WaitForFinish: true})
	p.Params = append(p.Params, packs.Parameter{Name: "answerDuration", Value: "17"})
	q, e := Compile(d, p.ID)
	if e != nil || q.Price != 0 || len(q.Question) != 2 || q.AnswerSeconds != 17 || q.Question[1].Placement != "replic" {
		t.Fatalf("%+v %v", q, e)
	}
	if !packs.Playable(Validate(d)) {
		t.Fatal(Validate(d))
	}
	if packs.Playable(ValidateLegacy(d)) {
		t.Fatal("legacy should remain strict")
	}
}
func TestV2CompilerDiagnosticsAndRanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*packs.Question)
		valid  bool
		code   string
	}{
		{"media", func(q *packs.Question) { q.Params[0].Items[0].Type = "audio" }, false, "game_media_pending"},
		{"unknown", func(q *packs.Question) { q.Params = append(q.Params, packs.Parameter{Name: "bogus"}) }, false, "game_parameter"},
		{"bad_duration", func(q *packs.Question) {
			q.Params = append(q.Params, packs.Parameter{Name: "answerDuration", Value: "4"})
		}, false, "game_answer_duration"},
		{"step_zero", func(q *packs.Question) {
			a, b, c := 100, 550, 0
			q.Type = "secret"
			q.Params = append(q.Params, packs.Parameter{Name: "price", Type: "numberSet", NumberSet: &packs.NumberSet{Minimum: &a, Maximum: &b, Step: &c}})
		}, true, ""},
		{"legacy_empty_price", func(q *packs.Question) {
			q.Type = "secret"
			q.Params = append(q.Params, packs.Parameter{Name: "price", Type: "numberSet", NumberSet: &packs.NumberSet{}})
		}, true, ""},
		{"bad_range", func(q *packs.Question) {
			a, b := 300, 100
			q.Type = "secret"
			q.Params = append(q.Params, packs.Parameter{Name: "price", Type: "numberSet", NumberSet: &packs.NumberSet{Minimum: &a, Maximum: &b}})
		}, false, "game_price_range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := examples.Demo()
			p := &d.Rounds[0].Themes[0].Questions[0]
			tc.mutate(p)
			_, err := Compile(d, p.ID)
			issues := Validate(d)
			if (err == nil) != tc.valid || packs.Playable(issues) != tc.valid {
				t.Fatalf("%v %+v", err, issues)
			}
			if !tc.valid {
				found := false
				for _, issue := range issues {
					if issue.Code == tc.code && issue.Path != "" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing actionable diagnostic: %+v", issues)
				}
			}
		})
	}
}
func TestV2RoundUsesPositiveMinimumAndFirstQuestion(t *testing.T) {
	d := examples.Demo()
	d.Rounds[0].Themes[0].Questions[0].Price = 0
	r, e := Round(d, d.Rounds[0].ID)
	if e != nil || r.MinimumPrice != 100 || r.MaximumPrice != 300 || r.Themes[0].QuestionID != "r1-t1-q1" {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestV2CompilerRejectsExplicitOrdinaryFinalType(t *testing.T) {
	d := examples.Demo()
	d.Rounds[0].Type = "final"
	if _, err := Compile(d, d.Rounds[0].Themes[0].Questions[0].ID); err == nil {
		t.Fatal("explicit ordinary final cannot enter final wagering state")
	}
}
func TestV2FinalNegativeSentinelAndMetadataBudget(t *testing.T) {
	d := examples.Demo()
	d.Rounds[0].Type = "final"
	q := &d.Rounds[0].Themes[0].Questions[0]
	q.Type = ""
	q.Price = -1
	compiled, err := Compile(d, q.ID)
	if err != nil || compiled.Price != 0 {
		t.Fatalf("final sentinel: %+v %v", compiled, err)
	}
	q.Price = -2
	if _, err = Compile(d, q.ID); err == nil {
		t.Fatal("other negative final price accepted")
	}
	q.Price = 0
	d.Rounds[0].Name = strings.Repeat("a", 257)
	if _, err = Round(d, d.Rounds[0].ID); err == nil {
		t.Fatal("oversized round title accepted")
	}
	d.Rounds[0].Name = "Final"
	for i := 0; i < 40; i++ {
		d.Rounds[0].Themes = append(d.Rounds[0].Themes, packs.Theme{ID: fmt.Sprintf("t%d", i), Name: strings.Repeat("T", 256), Questions: []packs.Question{{ID: fmt.Sprintf("q%d", i)}}})
	}
	if _, err = Round(d, d.Rounds[0].ID); err == nil {
		t.Fatal("oversized final theme projection accepted")
	}
}

func TestV2SourceRoundMinimumAndEmptySecretSelection(t *testing.T) {
	d := examples.Demo()
	for ti := range d.Rounds[0].Themes {
		for qi := range d.Rounds[0].Themes[ti].Questions {
			d.Rounds[0].Themes[ti].Questions[qi].Price = 0
		}
	}
	r, err := Round(d, d.Rounds[0].ID)
	if err != nil || r.MinimumPrice != 1 || r.MaximumPrice != 1 {
		t.Fatalf("positive fallback prices: %+v %v", r, err)
	}
	p := &d.Rounds[0].Themes[0].Questions[0]
	p.Type = "secret"
	q, err := Compile(d, p.ID)
	if err != nil || q.SelectionMode != "exceptCurrent" {
		t.Fatalf("secret default: %+v %v", q, err)
	}
}
