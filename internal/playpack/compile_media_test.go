package playpack

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/packs"
)

func readyMedia(kind string) game.MediaSpec {
	m := game.MediaSpec{Kind: kind, Standard: game.MediaVariant{Key: strings.Repeat("a", 64), Size: 2048}, Economy: game.MediaVariant{Key: strings.Repeat("b", 64), Size: 1024}}
	switch kind {
	case "image":
		m.Width = 640
		m.Height = 480
		m.Standard.MIME = "image/png"
	case "audio":
		m.DurationMS = 3000
		m.Standard.MIME = "audio/mp4"
	case "video":
		m.DurationMS = 3000
		m.Width = 640
		m.Height = 480
		m.Standard.MIME = "video/mp4"
	}
	m.Economy.MIME = m.Standard.MIME
	return m
}
func TestCompilePreparedMediaUsesOpaqueStableFragmentIDs(t *testing.T) {
	for _, kind := range []string{"image", "audio", "video"} {
		t.Run(kind, func(t *testing.T) {
			doc := examples.Demo()
			q := &doc.Rounds[0].Themes[0].Questions[0]
			q.Params[0].Items = []packs.Content{{Type: kind, Value: "PRIVATE-SOURCE.mp4", IsRef: true, DurationMS: 1500, WaitForFinish: true}}
			q.Params = append(q.Params, packs.Parameter{Name: "answer", Type: "content", Items: []packs.Content{{Type: kind, Value: "PRIVATE-ANSWER.mp4", IsRef: true}}})
			lookup := func(gotKind, reference string) (game.MediaSpec, error) {
				if gotKind != kind || !strings.HasPrefix(reference, "PRIVATE-") {
					t.Fatal("wrong lookup")
				}
				return readyMedia(kind), nil
			}
			a, err := CompileMedia(doc, q.ID, lookup)
			if err != nil {
				t.Fatal(err)
			}
			b, err := CompileMedia(doc, q.ID, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if a.Question[0].Media == nil || a.Question[0].Media.ID != b.Question[0].Media.ID || !game.ValidID(a.Question[0].Media.ID) || a.Question[0].Media.ID == a.Answer[0].Media.ID {
				t.Fatal("media IDs must be stable and distinct per fragment")
			}
			if strings.Contains(a.Question[0].Media.ID, "PRIVATE") || strings.Contains(a.Question[0].Media.ID, strings.Repeat("a", 32)) {
				t.Fatal("media ID contains source information")
			}
			if a.Question[0].Text != "" || a.Question[0].DurationMS != 1500 || !a.Question[0].WaitForFinish {
				t.Fatal("media timing/reference compiled as text")
			}
			encoded, _ := json.Marshal(a)
			if strings.Contains(string(encoded), "PRIVATE-SOURCE") {
				t.Fatal("original filename retained in private playable spec")
			}
			if !packs.Playable(ValidateMedia(doc, lookup)) {
				t.Fatal(ValidateMedia(doc, lookup))
			}
			if packs.Playable(Validate(doc)) {
				t.Fatal("unprepared profile silently accepted media")
			}
		})
	}
}
func TestCompileMediaMetadataAndReferenceDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content packs.Content
		lookup  AssetLookup
		code    string
	}{
		{"external", packs.Content{Type: "audio", Value: "https://example.invalid/file"}, func(string, string) (game.MediaSpec, error) {
			t.Fatal("external lookup called")
			return game.MediaSpec{}, nil
		}, "game_content"},
		{"missing", packs.Content{Type: "audio", Value: "missing", IsRef: true}, func(string, string) (game.MediaSpec, error) { return game.MediaSpec{}, errors.New("private/path") }, "game_media_pending"},
		{"invalid", packs.Content{Type: "video", Value: "bad", IsRef: true}, func(string, string) (game.MediaSpec, error) {
			m := readyMedia("video")
			m.DurationMS = 600001
			return m, nil
		}, "game_media_invalid"},
		{"wrong_kind", packs.Content{Type: "audio", Value: "wrong", IsRef: true}, func(string, string) (game.MediaSpec, error) { return readyMedia("image"), nil }, "game_media_invalid"},
		{"unsafe_key", packs.Content{Type: "image", Value: "bad", IsRef: true}, func(string, string) (game.MediaSpec, error) {
			m := readyMedia("image")
			m.Standard.Key = "../original"
			return m, nil
		}, "game_media_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := examples.Demo()
			q := &doc.Rounds[0].Themes[0].Questions[0]
			q.Params[0].Items = []packs.Content{tc.content}
			issues := ValidateMedia(doc, tc.lookup)
			if len(issues) != 1 || issues[0].Code != tc.code || !strings.Contains(issues[0].Path, "items[0]") || strings.Contains(issues[0].Message, "private/path") {
				t.Fatalf("%+v", issues)
			}
			if _, err := CompileMedia(doc, q.ID, tc.lookup); err == nil {
				t.Fatal("invalid media compiled")
			}
		})
	}
}
func TestMediaIDChangesWhenPreparedPinnedBytesChange(t *testing.T) {
	doc := examples.Demo()
	q := &doc.Rounds[0].Themes[0].Questions[0]
	q.Params[0].Items = []packs.Content{{Type: "image", Value: "image.png", IsRef: true}}
	a, err := CompileMedia(doc, q.ID, func(string, string) (game.MediaSpec, error) { return readyMedia("image"), nil })
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileMedia(doc, q.ID, func(string, string) (game.MediaSpec, error) {
		m := readyMedia("image")
		m.Standard.Key = strings.Repeat("c", 64)
		return m, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Question[0].Media.ID == b.Question[0].Media.ID {
		t.Fatal("changed pinned file reused media ID")
	}
}
