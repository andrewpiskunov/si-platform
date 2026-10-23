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

// Synthetic contract fixture; real captures currently expose no rating metadata.
func TestMetadataOptionalEngagement(t *testing.T) {
	b, _ := os.ReadFile("testdata/22419.html")
	base := string(b)
	// Insert inside the owning article, immediately before its title.
	pos := strings.Index(base, `<h1`)
	if pos < 0 {
		t.Fatal("missing fixture title")
	}
	markup := `<div itemprop="aggregateRating" itemscope itemtype="https://schema.org/AggregateRating"><meta itemprop="ratingValue" content="4.6"><meta itemprop="ratingCount" content="20"><meta itemprop="bestRating" content="5"><meta itemprop="worstRating" content="1"></div>`
	m, e := parseMetadata(strings.NewReader(base[:pos]+markup+base[pos:]), "22419")
	if e != nil || m.Rating == nil || *m.Rating != 4.6 || m.RatingCount == nil || *m.RatingCount != 20 {
		t.Fatal(m, e)
	}
	m, e = parseMetadata(strings.NewReader(base), "22419")
	if e != nil || m.Rating != nil || m.RatingCount != nil || m.Likes != nil {
		t.Fatal("invented values", m, e)
	}
	m, e = parseMetadata(strings.NewReader(base[:pos]+strings.Replace(markup, `content="5"`, `content="10"`, 1)+base[pos:]), "22419")
	if e != nil || m.Rating != nil {
		t.Fatal("wrong scale", m, e)
	}
}

func TestMetadataEngagementScopeAndLikes(t *testing.T) {
	b, _ := os.ReadFile("testdata/22419.html")
	base := string(b)
	pos := strings.Index(base, "<h1")
	markup := `<div itemprop="interactionStatistic"><link itemprop="interactionType" href="https://schema.org/LikeAction"><meta itemprop="userInteractionCount" content="12"></div>`
	m, e := parseMetadata(strings.NewReader(base[:pos]+markup+base[pos:]), "22419")
	if e != nil || m.Likes == nil || *m.Likes != 12 {
		t.Fatal(m, e)
	}
	m, e = parseMetadata(strings.NewReader(base[:pos]+"<article>"+markup+"</article>"+base[pos:]), "22419")
	if e != nil || m.Likes != nil {
		t.Fatal("recommended likes leaked", m, e)
	}
}
