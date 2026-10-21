package aiformat

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func request() Request {
	return Request{Mode: "theme", Topic: "History", Audience: "Adults", Difficulty: 3, Themes: 1, QuestionsPerTheme: 1}
}
func result() Result {
	return Result{Title: "Quiz", Themes: []Theme{{Name: "History", Questions: []Question{{Text: "Question?", Answer: "Answer", Comment: "Check this", Sources: []string{"http://127.0.0.1/private"}}}}}}
}
func TestValidation(t *testing.T) {
	r := request()
	if err := ValidateRequest(r); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Request){func(r *Request) { r.Mode = "bad" }, func(r *Request) { r.Themes = 2 }, func(r *Request) { r.QuestionsPerTheme = 11 }, func(r *Request) { r.Topic = strings.Repeat("я", 2001) }, func(r *Request) { r.Difficulty = 0 }} {
		x := r
		mutate(&x)
		if ValidateRequest(x) == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if err := ValidateResult(r, result()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Result){func(x *Result) { x.Title = " " }, func(x *Result) { x.Themes[0].Questions[0].Answer = "" }, func(x *Result) { x.Themes[0].Questions[0].Text = strings.Repeat("x", 4001) }, func(x *Result) { x.Themes = nil }, func(x *Result) { x.Themes[0].Questions[0].Sources = nil }} {
		x := result()
		mutate(&x)
		if ValidateResult(r, x) == nil {
			t.Fatal("invalid result accepted")
		}
	}
}
func TestDuplicates(t *testing.T) {
	r := request()
	r.QuestionsPerTheme = 2
	x := result()
	x.Themes[0].Questions = append(x.Themes[0].Questions, Question{Text: "  QUESTION?  ", Answer: "x", Sources: []string{}})
	if ValidateResult(r, x) == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestDocument(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	d := Document(result(), "job1", "model", now)
	if !reflect.DeepEqual(d, Document(result(), "job1", "model", now)) {
		t.Fatal("unstable ids")
	}
	q := d.Rounds[0].Themes[0].Questions[0]
	if q.Price != 100 || q.Type != "simple" || q.ID == "" || !strings.Contains(q.Info.Comments, "job1") || !strings.Contains(q.Info.Comments, "не проверены") || !strings.Contains(q.Info.Comments, "Check this") || q.Info.Sources[0] != "http://127.0.0.1/private" {
		t.Fatalf("bad document: %#v", q)
	}
	if q.ID == Document(result(), "job2", "model", now).Rounds[0].Themes[0].Questions[0].ID {
		t.Fatal("job ids collide")
	}
}
func TestRejectNonXMLText(t *testing.T) {
	for _, bad := range []string{"hello\x01", "hello\ufffe"} {
		x := result()
		x.Themes[0].Questions[0].Text = bad
		if ValidateResult(request(), x) == nil {
			t.Fatalf("non XML text accepted %q", bad)
		}
	}
}
