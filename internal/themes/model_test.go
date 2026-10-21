package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"siplatform/internal/media"
	"sort"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{SchemaVersion: 1, Name: "Морская тема", Author: "Автор", Provenance: "Собственные изображения; https://example.test", Font: "sans", Palette: Palette{Background: "#FFFFFF", Surface: "#FFFFFF", SurfaceAlt: "#FFFFFF", Border: "#000000", Text: "#000000", Muted: "#000000", Accent: "#000000", AccentText: "#FFFFFF", Danger: "#000000", Success: "#000000", Focus: "#000000"}, Cards: Shape{Radius: 8, BorderWidth: 1}, Buttons: Shape{Radius: 8, BorderWidth: 1}}
}

// A corrupt saved result must never cross the trusted storage boundary.
func TestPreparedRejectsIncompleteAndExposesSafeIssues(t *testing.T) {
	p := Prepared{Manifest: validManifest(), OriginalKey: strings.Repeat("a", 64), OriginalSize: 100, SizeBytes: 100}
	if err := p.Validate(); !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
		t.Fatalf("incomplete result: %v", err)
	}
	if got := Issues(errors.New("private /tmp/path")); len(got) != 0 {
		t.Fatalf("private issue leaked: %v", got)
	}
	err := &ValidationError{Issues: []Issue{{Path: "theme.json", Code: "invalid", Message: "Некорректная тема"}}}
	if !errors.Is(err, ErrInvalid) || len(Issues(err)) != 1 {
		t.Fatalf("validation error contract: %v", err)
	}
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"schema_version", "surface_alt", "accent_text", "border_width", "original_key", "original_size", "size_bytes"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Fatalf("missing JSON field %q: %s", name, raw)
		}
	}
}

// Saved data must be revalidated even when its manifest hash was recomputed.
func TestPreparedValidatesProfileHashAndLogicalBytes(t *testing.T) {
	p := preparedFixture(t)
	if err := p.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := []struct {
		name   string
		change func(*Prepared)
	}{
		{"hash", func(p *Prepared) { p.Hash = strings.Repeat("f", 64) }},
		{"archive key", func(p *Prepared) { p.OriginalKey = "other" }},
		{"archive size", func(p *Prepared) { p.OriginalSize = 5<<20 + 1; p.SizeBytes = p.OriginalSize }},
		{"logical size", func(p *Prepared) { p.SizeBytes++ }},
		{"source missing", func(p *Prepared) { p.Manifest.Logo = "assets/logo.png" }},
		{"unknown asset", func(p *Prepared) { p.Assets = []Asset{imageAsset("assets/a.png", 10)} }},
		{"invalid manifest", func(p *Prepared) { p.Manifest.Font = "external" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := p
			c.change(&q)
			if c.name != "hash" {
				q.Hash = fixtureHash(t, q)
			}
			if err := q.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid %s: %+v", c.name, q)
			}
		})
	}
	p.Manifest.Logo = "assets/a.png"
	p.Assets = []Asset{imageAsset("assets/a.png", 10)}
	p.SizeBytes = 120
	p.Hash = fixtureHash(t, p)
	if err := p.Validate(); err != nil {
		t.Fatalf("valid image: %v", err)
	}
	for _, c := range []struct {
		name   string
		change func(*Prepared)
	}{
		{"dimension", func(p *Prepared) { p.Assets[0].Prepared.Width = 4000001 }},
		{"duration", func(p *Prepared) { p.Assets[0].Prepared.DurationMS = 1 }},
		{"kind", func(p *Prepared) { p.Assets[0].Prepared.Kind = "video" }},
		{"mime", func(p *Prepared) { p.Assets[0].Prepared.Standard.MIME = "image/svg+xml" }},
		{"empty size", func(p *Prepared) { p.Assets[0].Prepared.Standard.Size = 0 }},
		{"standard budget", func(p *Prepared) { p.Assets[0].Prepared.Standard.Size = 2<<20 + 1 }},
		{"economy budget", func(p *Prepared) { p.Assets[0].Prepared.Economy.Size = 1<<20 + 1 }},
		{"key", func(p *Prepared) { p.Assets[0].Prepared.Standard.Key = "../../escape" }},
		{"duplicate", func(p *Prepared) { p.Assets = append(p.Assets, p.Assets[0]) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := p
			q.Assets = append([]Asset(nil), p.Assets...)
			c.change(&q)
			q.SizeBytes = q.OriginalSize
			for _, a := range q.Assets {
				q.SizeBytes += a.Prepared.Standard.Size + a.Prepared.Economy.Size
			}
			q.Hash = fixtureHash(t, q)
			if err := q.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid %s", c.name)
			}
		})
	}
}

func preparedFixture(t *testing.T) Prepared {
	t.Helper()
	p := Prepared{Manifest: validManifest(), Assets: []Asset{}, OriginalKey: strings.Repeat("a", 64), OriginalSize: 100, SizeBytes: 100}
	p.Hash = fixtureHash(t, p)
	return p
}
func fixtureHash(t *testing.T, p Prepared) string {
	t.Helper()
	assets := append([]Asset{}, p.Assets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	b, err := json.Marshal(struct {
		Manifest Manifest `json:"manifest"`
		Assets   []Asset  `json:"assets"`
	}{p.Manifest, assets})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func imageAsset(name string, size int64) Asset {
	return Asset{Name: name, Prepared: media.Prepared{Kind: "image", Width: 1, Height: 1, Standard: media.Variant{Key: strings.Repeat("b", 64), MIME: "image/png", Size: size}, Economy: media.Variant{Key: strings.Repeat("b", 64), MIME: "image/png", Size: size}}}
}

func TestPreparedRejectsConflictingSizesForSameContentHash(t *testing.T) {
	p := preparedFixture(t)
	p.Manifest.Logo = "assets/a.png"
	p.Manifest.Background = "assets/b.png"
	p.Assets = []Asset{imageAsset("assets/a.png", 10), imageAsset("assets/b.png", 11)}
	p.SizeBytes = 142
	p.Hash = fixtureHash(t, p)
	if err := p.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("same immutable content key accepted with two different sizes")
	}
}
