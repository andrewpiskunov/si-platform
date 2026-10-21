package themeexample

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"siplatform/internal/media"
	"siplatform/internal/themes"
)

// A regenerated download must be byte-identical and contain the editable source
// manifest plus our PNG; no build timestamps or machine paths enter the archive.
func TestExampleReproducibleAndMatchesPublishedFiles(t *testing.T) {
	first, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("theme ZIP changes between builds")
	}
	checked, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "themes", "example.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, checked) {
		t.Fatal("published ZIP is stale; run go run ./cmd/theme-example")
	}
	source, err := os.ReadFile("theme.json")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("unexpected archive entries: %d", len(zr.File))
	}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		switch f.Name {
		case "theme.json":
			if !bytes.Equal(source, data) {
				t.Fatal("source manifest and ZIP differ")
			}
		case "assets/compass.png":
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width != 128 || config.Height != 128 {
				t.Fatalf("invalid original illustration: %+v %v", config, err)
			}
		default:
			t.Fatalf("unexpected entry %s", f.Name)
		}
	}
	publishedGuide, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "themes", "guide.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Guide(), publishedGuide) {
		t.Fatal("published guide is stale; run go run ./cmd/theme-example")
	}
}

func TestExampleParsesAndPreparesInRealSandbox(t *testing.T) {
	data, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := themes.Read(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("example rejected: %v %+v", err, themes.Issues(err))
	}
	if os.Getenv("MEDIA_INTEGRATION") != "1" {
		t.Skip("set MEDIA_INTEGRATION=1 to verify example in real media sandbox")
	}
	processor, err := media.New(media.Options{FFmpeg: os.Getenv("MEDIA_FFMPEG"), FFprobe: os.Getenv("MEDIA_FFPROBE"), Sandbox: "/usr/bin/bwrap", Prlimit: "/usr/bin/prlimit", Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := themes.Prepare(context.Background(), processor, archive)
	if err != nil {
		t.Fatalf("example preparation: %v %+v", err, themes.Issues(err))
	}
	defer result.Close()
	if err = result.Prepared.Validate(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if result.Prepared.OriginalKey != hex.EncodeToString(sum[:]) || result.Prepared.OriginalSize != int64(len(data)) {
		t.Fatal("example source hash or size changed")
	}
	if len(result.Prepared.Assets) != 1 || len(result.Files) != 2 {
		t.Fatal("example image not prepared in both qualities")
	}
	asset := result.Prepared.Assets[0]
	if asset.Name != "assets/compass.png" || asset.Prepared.Width != 128 || asset.Prepared.Height != 128 {
		t.Fatalf("unexpected prepared example: %+v", asset)
	}
	if result.Prepared.SizeBytes > 32<<10 {
		t.Fatalf("example unnecessarily large: %d bytes", result.Prepared.SizeBytes)
	}
	t.Logf("ZIP=%d bytes; standard=%d; economy=%d; logical=%d", len(data), asset.Prepared.Standard.Size, asset.Prepared.Economy.Size, result.Prepared.SizeBytes)
}
