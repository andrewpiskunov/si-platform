// Package aiformat defines bounded, untrusted AI suggestions.
package aiformat

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"siplatform/internal/packs"
	"strings"
	"time"
	"unicode/utf8"
)

const Version = "ai-authoring-v1"

type Request struct {
	Mode              string `json:"mode"`
	Topic             string `json:"topic"`
	Audience          string `json:"audience"`
	Difficulty        int    `json:"difficulty"`
	Themes            int    `json:"themes"`
	QuestionsPerTheme int    `json:"questions_per_theme"`
}
type Result struct {
	Title  string  `json:"title"`
	Themes []Theme `json:"themes"`
}
type Theme struct {
	Name      string     `json:"name"`
	Questions []Question `json:"questions"`
}
type Question struct {
	Text    string   `json:"text"`
	Answer  string   `json:"answer"`
	Comment string   `json:"comment"`
	Sources []string `json:"sources"`
}

var ErrInvalid = errors.New("invalid AI format")

func textOK(s string, max int, required bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || (required && strings.TrimSpace(s) == "") {
		return false
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xfffe || r == 0xffff {
			return false
		}
	}
	return true
}
func ValidateRequest(r Request) error {
	if (r.Mode != "theme" && r.Mode != "pack") || !textOK(r.Topic, 2000, true) || !textOK(r.Audience, 200, true) || r.Difficulty < 1 || r.Difficulty > 5 || r.Themes < 1 || r.Themes > 10 || r.QuestionsPerTheme < 1 || r.QuestionsPerTheme > 10 || (r.Mode == "theme" && r.Themes != 1) {
		return ErrInvalid
	}
	return nil
}
func ValidateResult(r Request, v Result) error {
	if ValidateRequest(r) != nil || !textOK(v.Title, 200, true) || len(v.Themes) != r.Themes {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, t := range v.Themes {
		if !textOK(t.Name, 200, true) || len(t.Questions) != r.QuestionsPerTheme {
			return ErrInvalid
		}
		for _, q := range t.Questions {
			if !textOK(q.Text, 4000, true) || !textOK(q.Answer, 1000, true) || !textOK(q.Comment, 2000, false) || q.Sources == nil || len(q.Sources) > 5 {
				return ErrInvalid
			}
			for _, s := range q.Sources {
				if !textOK(s, 1000, true) {
					return ErrInvalid
				}
			}
			key := strings.ToLower(strings.Join(strings.Fields(q.Text), " "))
			if seen[key] {
				return ErrInvalid
			}
			seen[key] = true
		}
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) > 1<<20 {
		return ErrInvalid
	}
	return nil
}
func Document(v Result, jobID, model string, generatedAt time.Time) packs.Document {
	id := func(s string) string { return fmt.Sprintf("ai-%x", sha256.Sum256([]byte(jobID+":"+s)))[:35] }
	d := packs.Document{ID: id("pack"), Name: v.Title, Version: "5.0", Language: "ru", Date: generatedAt.UTC().Format("2006-01-02"), Generator: Version}
	round := packs.Round{ID: id("round"), Name: "Раунд 1", Type: "standard"}
	for ti, t := range v.Themes {
		pt := packs.Theme{ID: id(fmt.Sprint("theme", ti)), Name: t.Name}
		for qi, q := range t.Questions {
			provenance := fmt.Sprintf("ИИ: job_id=%s; model=%s; generated_at=%s; факты/источники не проверены", jobID, model, generatedAt.UTC().Format(time.RFC3339))
			comment := provenance
			if q.Comment != "" {
				comment = q.Comment + "\n" + provenance
			}
			pt.Questions = append(pt.Questions, packs.Question{ID: id(fmt.Sprintf("question:%d:%d", ti, qi)), Price: (qi + 1) * 100, Type: "simple", Info: packs.Info{Sources: append([]string(nil), q.Sources...), Comments: comment}, Right: []string{q.Answer}, Params: []packs.Parameter{{Name: "question", Type: "content", Items: []packs.Content{{Type: "text", Value: q.Text}}}}})
		}
		round.Themes = append(round.Themes, pt)
	}
	d.Rounds = []packs.Round{round}
	return d
}

// UnmarshalJSON preserves the distinction between required empty strings and
// absent/null model fields; the same constraint applies to edited suggestions.
func (q *Question) UnmarshalJSON(b []byte) error {
	var v struct {
		Text    *string  `json:"text"`
		Answer  *string  `json:"answer"`
		Comment *string  `json:"comment"`
		Sources []string `json:"sources"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || v.Text == nil || v.Answer == nil || v.Comment == nil || v.Sources == nil {
		return ErrInvalid
	}
	*q = Question{Text: *v.Text, Answer: *v.Answer, Comment: *v.Comment, Sources: v.Sources}
	return nil
}
