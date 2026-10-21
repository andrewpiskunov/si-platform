package themes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"siplatform/internal/media"
)

func integrationProcessor(t *testing.T) (*media.Processor, string) {
	t.Helper()
	if os.Getenv("MEDIA_INTEGRATION") != "1" {
		t.Skip("set MEDIA_INTEGRATION=1 for real sandbox image preparation")
	}
	dir := t.TempDir()
	p, err := media.New(media.Options{FFmpeg: os.Getenv("MEDIA_FFMPEG"), FFprobe: os.Getenv("MEDIA_FFPROBE"), Sandbox: "/usr/bin/bwrap", Prlimit: "/usr/bin/prlimit", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	return p, dir
}
func fixtureImage(t *testing.T, w, h int, format string, noise bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	seed := uint32(17)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{uint8(x), uint8(y), 27, 255}
			if noise {
				seed = seed*1664525 + 1013904223
				c = color.NRGBA{uint8(seed >> 24), uint8(seed >> 16), uint8(seed >> 8), 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 75})
	} else {
		err = png.Encode(&b, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func fixtureWebP(t *testing.T, animated bool) []byte {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "image.webp")
	args := []string{"-v", "error", "-nostdin", "-threads", "1", "-f", "lavfi", "-i", "testsrc=size=64x32:rate=2:duration=1", "-frames:v", "1", "-c:v", "libwebp", "-f", "webp", filename}
	if animated {
		args = []string{"-v", "error", "-nostdin", "-threads", "1", "-f", "lavfi", "-i", "testsrc=size=64x32:rate=2:duration=1", "-c:v", "libwebp_anim", "-f", "webp", filename}
	}
	if out, err := exec.Command(os.Getenv("MEDIA_FFMPEG"), args...).CombinedOutput(); err != nil {
		t.Fatalf("webp fixture: %v %s", err, out)
	}
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func imageArchive(t *testing.T, images map[string][]byte) *Archive {
	t.Helper()
	m := validManifest()
	slots := []*string{&m.Background, &m.Logo, &m.Ornament}
	entries := []zipEntry{}
	i := 0
	for name, b := range images {
		*slots[i] = name
		i++
		entries = append(entries, zipEntry{name: name, data: b})
	}
	entries = append(entries, zipEntry{name: "theme.json", data: manifestBytes(t, m)})
	a, err := readFixture(t, zipFixture(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestPrepareActualThreeFormatsVerifiesOutputsAndCleansPrivateFiles(t *testing.T) {
	p, dir := integrationProcessor(t)
	keep := filepath.Join(dir, "keep")
	if err := os.WriteFile(keep, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	a := imageArchive(t, map[string][]byte{"assets/a.png": fixtureImage(t, 64, 32, "png", false), "assets/b.jpeg": fixtureImage(t, 64, 32, "jpeg", false), "assets/c.webp": fixtureWebP(t, false)})
	result, err := Prepare(context.Background(), p, a)
	if err != nil {
		t.Fatalf("real prepare: %v issues=%+v", err, Issues(err))
	}
	defer result.Close()
	if len(result.Prepared.Assets) != 3 || len(result.Files) != 6 {
		t.Fatalf("missing outputs: %+v", result)
	}
	if err = result.Prepared.Validate(); err != nil {
		t.Fatal(err)
	}
	if result.Prepared.OriginalKey != a.originalKey || result.Prepared.OriginalSize != a.originalSize {
		t.Fatal("source metadata changed")
	}
	total := result.Prepared.OriginalSize
	for _, asset := range result.Prepared.Assets {
		if asset.Prepared.Width != 64 || asset.Prepared.Height != 32 {
			t.Fatalf("wrong dimensions: %+v", asset)
		}
		total += asset.Prepared.Standard.Size + asset.Prepared.Economy.Size
	}
	if total != result.Prepared.SizeBytes {
		t.Fatal("logical byte count excludes an output")
	}
	for _, out := range result.Files {
		b, err := os.ReadFile(out.Path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if out.Key != hex.EncodeToString(sum[:]) || out.Size != int64(len(b)) || out.MIME != "image/png" {
			t.Fatalf("unverified output: %+v", out)
		}
		if _, err := png.Decode(bytes.NewReader(b)); err != nil {
			t.Fatalf("output not decodable PNG: %v", err)
		}
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	for _, out := range result.Files {
		if _, err := os.Stat(out.Path); !os.IsNotExist(err) {
			t.Fatal("output survived Close")
		}
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "unrelated" {
		t.Fatal("Close touched unrelated file")
	}
}
func TestPrepareRepeatedReferenceProcessesOneAsset(t *testing.T) {
	p, _ := integrationProcessor(t)
	m := validManifest()
	m.Background = "assets/a.png"
	m.Logo = m.Background
	m.Ornament = m.Background
	a, err := readFixture(t, zipFixture(t, zipEntry{name: "theme.json", data: manifestBytes(t, m)}, zipEntry{name: m.Logo, data: fixtureImage(t, 16, 16, "png", false)}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Prepare(context.Background(), p, a)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.Prepared.Assets) != 1 || len(r.Files) != 2 {
		t.Fatal("reused asset counted more than once")
	}
}
func TestPrepareRejectsAnimationMalformedAndOverFourMegapixels(t *testing.T) {
	p, dir := integrationProcessor(t)
	pngBytes := fixtureImage(t, 32, 32, "png", false)
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	animated := append(append(append([]byte{}, pngBytes[:33]...), chunk...), pngBytes[33:]...)
	for _, c := range []struct {
		name string
		data []byte
	}{{"animated.png", animated}, {"animated.webp", fixtureWebP(t, true)}, {"bad.png", pngBytes[:len(pngBytes)/2]}, {"bad.jpeg", []byte("<svg><script>bad</script></svg>")}, {"huge.png", fixtureImage(t, 2001, 2000, "png", false)}, {"huge.jpeg", fixtureImage(t, 2001, 2000, "jpeg", false)}} {
		t.Run(c.name, func(t *testing.T) {
			a := imageArchive(t, map[string][]byte{"assets/" + c.name: c.data})
			r, err := Prepare(context.Background(), p, a)
			if r != nil {
				r.Close()
			}
			if !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
				t.Fatalf("unsafe image accepted: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed preparation leaked files: %v %+v", err, entries)
			}
		})
	}
}
func TestPrepareRejectsAggregateEconomyBudgetAndCleansEarlierImages(t *testing.T) {
	p, dir := integrationProcessor(t)
	data := fixtureImage(t, 512, 512, "jpeg", true)
	a := imageArchive(t, map[string][]byte{"assets/a.jpeg": data, "assets/b.jpeg": data})
	r, err := Prepare(context.Background(), p, a)
	if r != nil {
		r.Close()
	}
	issues := Issues(err)
	if !errors.Is(err, ErrInvalid) || len(issues) == 0 || issues[0].Code != "budget" {
		t.Fatalf("aggregate economy budget accepted or wrong failure: %v %+v", err, issues)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("budget failure leaked files: %v %+v", err, entries)
	}
}
func TestPrepareActualCancellationCleansFiles(t *testing.T) {
	p, dir := integrationProcessor(t)
	a := imageArchive(t, map[string][]byte{"assets/a.png": fixtureImage(t, 1800, 1800, "png", false)})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	r, err := Prepare(ctx, p, a)
	if r != nil {
		r.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("cancel leaked files: %v %+v", e, entries)
	}
}
func TestPreparedCanonicalAssetOrder(t *testing.T) {
	p := preparedFixture(t)
	p.Manifest.Background = "assets/z.png"
	p.Manifest.Logo = "assets/a.png"
	p.Assets = []Asset{imageAsset("assets/z.png", 10), imageAsset("assets/a.png", 10)}
	p.SizeBytes = 140
	p.Hash = fixtureHash(t, p)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Assets[0], p.Assets[1] = p.Assets[1], p.Assets[0]
	if err := p.Validate(); err != nil {
		t.Fatal("canonical hash depends on asset insertion order")
	}
	p.OriginalKey = strings.Repeat("c", 64)
	p.OriginalSize++
	p.SizeBytes++
	if err := p.Validate(); err != nil {
		t.Fatal("canonical hash depends on original archive")
	}
}

func TestPrepareAcceptsExactlyFourMegapixels(t *testing.T) {
	p, _ := integrationProcessor(t)
	a := imageArchive(t, map[string][]byte{"assets/a.png": fixtureImage(t, 2000, 2000, "png", false)})
	r, err := Prepare(context.Background(), p, a)
	if err != nil {
		t.Fatalf("4 MP boundary rejected: %v %+v", err, Issues(err))
	}
	defer r.Close()
	if r.Prepared.Assets[0].Prepared.Width != 2000 || r.Prepared.Assets[0].Prepared.Height != 2000 {
		t.Fatal("standard dimensions changed")
	}
}

func TestPrepareRejectsWebPOverFourMegapixelsBeforeProcessing(t *testing.T) {
	p, _ := integrationProcessor(t)
	filename := filepath.Join(t.TempDir(), "large.webp")
	args := []string{"-v", "error", "-nostdin", "-threads", "1", "-f", "lavfi", "-i", "color=size=2002x2000:color=blue", "-frames:v", "1", "-c:v", "libwebp", "-f", "webp", filename}
	if out, err := exec.Command(os.Getenv("MEDIA_FFMPEG"), args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	a := imageArchive(t, map[string][]byte{"assets/a.webp": b})
	r, err := Prepare(context.Background(), p, a)
	if r != nil {
		r.Close()
	}
	if !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
		t.Fatalf("large WebP accepted: %v", err)
	}
}

// A wide grayscale image keeps its economy output small while its standard PNG
// exceeds 2 MiB, exercising that budget independently of the economy limit.
func TestPrepareRejectsStandardBudgetEvenWhenEconomyFits(t *testing.T) {
	p, dir := integrationProcessor(t)
	img := image.NewGray(image.Rect(0, 0, 4000, 1000))
	seed := uint32(101)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
	}
	var source bytes.Buffer
	if err := jpeg.Encode(&source, img, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatal(err)
	}
	if source.Len() > 2<<20 {
		t.Fatal("fixture source exceeds theme limit")
	}
	control, err := p.Prepare(context.Background(), "image", bytes.NewReader(source.Bytes()), int64(source.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if control.Prepared.Standard.Size <= 2<<20 || control.Prepared.Economy.Size > 1<<20 {
		control.Close()
		t.Fatalf("fixture does not isolate standard budget: %+v", control.Prepared)
	}
	if err = control.Close(); err != nil {
		t.Fatal(err)
	}
	a := imageArchive(t, map[string][]byte{"assets/wide.jpeg": source.Bytes()})
	r, err := Prepare(context.Background(), p, a)
	if r != nil {
		r.Close()
	}
	issues := Issues(err)
	if !errors.Is(err, ErrInvalid) || len(issues) == 0 || issues[0].Code != "budget" {
		t.Fatalf("standard-only excess accepted: %v %+v", err, issues)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("standard budget failure leaked files: %v %+v", e, entries)
	}
}
