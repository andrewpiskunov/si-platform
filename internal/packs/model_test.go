package packs

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func validDocument() Document {
	return Document{ID: "test", Name: "Пакет", Version: "5.0", Language: "ru", Info: Info{Authors: []string{"Автор"}, Sources: []string{"https://example.org"}}, Rounds: []Round{{ID: "r1", Name: "Раунд", Themes: []Theme{{ID: "t1", Name: "Тема", Questions: []Question{{ID: "q1", Price: 100, Type: "simple", Right: []string{"Ответ"}, Info: Info{ShowmanComments: "Секрет"}, Params: []Parameter{{Name: "question", Type: "content", Items: []Content{{Type: "text", Value: "Вопрос?"}, {Type: "image", Value: "image.png", IsRef: true, DurationMS: 2500, WaitForFinish: true}}}}}}}}}}}
}
func TestDocumentRoundTrip(t *testing.T) {
	doc := validDocument()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var next Document
	if err = json.Unmarshal(b, &next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc, next) {
		t.Fatal("model lost data")
	}
	if issues := Validate(next); !Playable(issues) {
		t.Fatalf("valid: %+v", issues)
	}
	items := next.Rounds[0].Themes[0].Questions[0].Content("question")
	if len(items) != 2 || items[1].DurationMS != 2500 || items[0].Value != "Вопрос?" {
		t.Fatalf("content order: %+v", items)
	}
}
func TestQuestionTypes(t *testing.T) {
	for _, kind := range []string{"", "simple", "withButton", "stake", "stakeAll", "secret", "forAll", "noRisk"} {
		t.Run(kind, func(t *testing.T) {
			doc := validDocument()
			doc.Rounds[0].Themes[0].Questions[0].Type = kind
			if issues := Validate(doc); !Playable(issues) {
				t.Fatalf("P0 type rejected: %+v", issues)
			}
		})
	}
}
func TestValidationReportsPaths(t *testing.T) {
	cases := []struct {
		name, code string
		mutate     func(*Document)
	}{
		{"name", "required", func(d *Document) { d.Name = " " }},
		{"answer", "required", func(d *Document) { d.Rounds[0].Themes[0].Questions[0].Right = nil }},
		{"type", "unsupported_type", func(d *Document) { d.Rounds[0].Themes[0].Questions[0].Type = "custom" }},
		{"param", "unsupported_parameter", func(d *Document) {
			q := &d.Rounds[0].Themes[0].Questions[0]
			q.Params = append(q.Params, Parameter{Name: "unknown", Value: "surprise"})
		}},
		{"html", "unsupported_content", func(d *Document) { d.Rounds[0].Themes[0].Questions[0].Params[0].Items[0].Type = "html" }},
		{"script", "unsupported_script", func(d *Document) { d.Rounds[0].Themes[0].Questions[0].HasScript = true }},
		{"duration", "invalid_duration", func(d *Document) { d.Rounds[0].Themes[0].Questions[0].Params[0].Items[0].DurationMS = -1 }},
		{"duplicate", "duplicate_id", func(d *Document) { th := &d.Rounds[0].Themes[0]; th.Questions = append(th.Questions, th.Questions[0]) }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDocument()
			tt.mutate(&doc)
			issues := Validate(doc)
			found := false
			for _, i := range issues {
				if i.Code == tt.code && i.Path != "" && i.Message != "" {
					found = true
				}
			}
			if !found || Playable(issues) {
				t.Fatalf("expected blocking %s: %+v", tt.code, issues)
			}
		})
	}
}
func TestQuestionLimitAndImportIssues(t *testing.T) {
	d := validDocument()
	q := d.Rounds[0].Themes[0].Questions[0]
	d.Rounds[0].Themes[0].Questions = make([]Question, 2001)
	for i := range d.Rounds[0].Themes[0].Questions {
		n := q
		n.ID = strings.Repeat("x", i+1)
		d.Rounds[0].Themes[0].Questions[i] = n
	}
	found := false
	for _, issue := range Validate(d) {
		if issue.Code == "question_limit" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing 2000-question cap")
	}
	d = validDocument()
	d.ImportIssues = []Issue{{Severity: "error", Code: "unknown_xml", Path: "package/custom", Message: "Unsupported XML"}}
	if Playable(Validate(d)) {
		t.Fatal("import diagnostics disappeared")
	}
}
