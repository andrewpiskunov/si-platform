package siq

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"siplatform/internal/packs"
)

func TestExportSemanticRoundTrip(t *testing.T) {
	for name, fixture := range map[string]string{"v4": v4, "v5": v5, "nested": strings.Replace(v5, `</params>`, `<param name="price" type="numberSet"><numberSet minimum="100" maximum="500" step="100"/></param><param name="theme" type="group"><param name="label">Текст</param></param></params>`, 1)} {
		t.Run(name, func(t *testing.T) {
			a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0}, entry{"Audio/песня.mp3", []byte("audio"), 0}))
			var out bytes.Buffer
			if err := Write(context.Background(), &out, a.Document, a.Assets); err != nil {
				t.Fatal(err)
			}
			b := readArchive(t, out.Bytes())
			a.Document.SourceVersion = b.Document.SourceVersion
			if !reflect.DeepEqual(a.Document, b.Document) {
				t.Fatalf("semantic loss:\nbefore=%+v\nafter=%+v", a.Document, b.Document)
			}
			for i, asset := range a.Assets {
				if b.Assets[i].SHA256 != asset.SHA256 {
					t.Fatal("media bytes changed")
				}
			}
		})
	}
}
func TestExportRejectsLossAndBadAssets(t *testing.T) {
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(v5), 0}, entry{"Images/кот.png", []byte("image"), 0}))
	cases := []struct {
		name   string
		mutate func(*packs.Document, *[]Asset)
	}{
		{"unknown import", func(d *packs.Document, a *[]Asset) {
			d.ImportIssues = []packs.Issue{{Severity: "error", Code: "unsupported_xml", Path: "x", Message: "Unsupported"}}
		}},
		{"missing media", func(d *packs.Document, a *[]Asset) { *a = nil }},
		{"traversal", func(d *packs.Document, a *[]Asset) { (*a)[0].Name = "../file" }},
		{"changed content", func(d *packs.Document, a *[]Asset) {
			(*a)[0].Open = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("wrong")), nil }
		}},
		{"wrong size", func(d *packs.Document, a *[]Asset) { (*a)[0].Size = 100 }},
		{"XML injection", func(d *packs.Document, a *[]Asset) { d.GlobalXML = `<!DOCTYPE package><global/>` }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			d := a.Document
			assets := append([]Asset(nil), a.Assets...)
			tt.mutate(&d, &assets)
			var out bytes.Buffer
			if err := Write(context.Background(), &out, d, assets); err == nil {
				t.Fatal("invalid export accepted")
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk failed") }
func TestExportErrorsAndCancellation(t *testing.T) {
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(v5), 0}, entry{"Images/кот.png", []byte("image"), 0}))
	if err := Write(context.Background(), brokenWriter{}, a.Document, a.Assets); err == nil {
		t.Fatal("write error ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Write(ctx, io.Discard, a.Document, a.Assets); err == nil {
		t.Fatal("cancel ignored")
	}
}
