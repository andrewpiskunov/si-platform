package sibrowser

import (
	"os"
	"strings"
	"testing"
)

func TestMetadataRealCards(t *testing.T) {
	for _, id := range []string{"22419", "25928", "25924"} {
		b, e := os.ReadFile("testdata/" + id + ".html")
		if e != nil {
			t.Fatal(e)
		}
		m, e := parseMetadata(strings.NewReader(string(b)), id)
		if e != nil {
			t.Fatal(id, e)
		}
		if m.Name == "" || m.Filename == "" || m.PublishedDate == "" || len(m.Authors) == 0 || len(m.Media) != 4 || len(m.Categories) != 6 || len(m.Originals) != 1 || len(m.Rounds) == 0 {
			t.Fatalf("%s: %+v", id, m)
		}
		if id == "22419" && (len(m.Authors) != 3 || m.CoverURL != Origin+"/packages/22419/logo.webp") {
			t.Fatal(m)
		}
		if id == "25928" && (m.CoverURL != "" || len(m.Authors) != 1 || m.QuestionCount == nil || *m.QuestionCount != 103 || len(m.Description) != 1) {
			t.Fatal(m)
		}
		if id == "25924" && (len(m.Tags) != 1 || m.Tags[0].Name != "Общие знания") {
			t.Fatal(m)
		}
	}
}
func TestMetadataRejectsWrongPackage(t *testing.T) {
	b, _ := os.ReadFile("testdata/22419.html")
	if _, e := parseMetadata(strings.NewReader(string(b)), "999"); e == nil {
		t.Fatal("wrong package accepted")
	}
}
