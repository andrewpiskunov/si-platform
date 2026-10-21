package themes

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func zipFixture(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func manifestBytes(t *testing.T, m Manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func readFixture(t *testing.T, b []byte) (*Archive, error) {
	t.Helper()
	return Read(context.Background(), bytes.NewReader(b), int64(len(b)))
}
func TestReadAndPreparePaletteOnlyPreservesSourceAndIgnoresZIPPackagingInHash(t *testing.T) {
	m := validManifest()
	b := zipFixture(t, zipEntry{name: "theme.json", data: manifestBytes(t, m)})
	a, err := readFixture(t, b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Prepare(context.Background(), nil, a)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	h := sha256.Sum256(b)
	if r.Prepared.OriginalKey != hex.EncodeToString(h[:]) || r.Prepared.OriginalSize != int64(len(b)) || r.Prepared.SizeBytes != int64(len(b)) || len(r.Files) != 0 {
		t.Fatalf("source not preserved: %+v", r)
	}
	if err = r.Prepared.Validate(); err != nil {
		t.Fatal(err)
	}
	b2 := zipFixture(t, zipEntry{name: "assets/", mode: os.ModeDir | 0755}, zipEntry{name: "theme.json", data: append(manifestBytes(t, m), '\n')})
	a2, err := readFixture(t, b2)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Prepare(context.Background(), nil, a2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if r.Prepared.Hash != r2.Prepared.Hash || r.Prepared.OriginalKey == r2.Prepared.OriginalKey {
		t.Fatal("version hash depends on ZIP packaging")
	}
}
func TestReadStrictManifest(t *testing.T) {
	valid := string(manifestBytes(t, validManifest()))
	cases := map[string]string{
		"unknown":           strings.Replace(valid, `"name":`, `"extra":0,"name":`, 1),
		"duplicate":         strings.Replace(valid, `"name":`, `"name":"first","name":`, 1),
		"escaped duplicate": strings.Replace(valid, `"name":`, `"na\u006de":"first","name":`, 1),
		"nested duplicate":  strings.Replace(valid, `"radius":8`, `"radius":9,"radius":8`, 1),
		"wrong field case":  strings.Replace(valid, `"name":`, `"Name":`, 1),
		"nested unknown":    strings.Replace(valid, `"radius":8`, `"other":9,"radius":8`, 1),
		"null":              strings.Replace(valid, `"author":"Автор"`, `"author":null`, 1),
		"trailing":          valid + ` {}`,
		"version":           strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		"font":              strings.Replace(valid, `"sans"`, `"url(external)"`, 1),
		"color":             strings.Replace(valid, `#FFFFFF`, `#FFF`, 1),
		"transparent":       strings.Replace(valid, `#FFFFFF`, `#FFFFFF00`, 1),
		"radius":            strings.Replace(valid, `"radius":8`, `"radius":25`, 1),
		"border width":      strings.Replace(valid, `"border_width":1`, `"border_width":0`, 1),
		"blank name":        strings.Replace(valid, `Морская тема`, `   `, 1),
		"name length":       strings.Replace(valid, `Морская тема`, strings.Repeat("я", 81), 1),
		"author length":     strings.Replace(valid, `Автор`, strings.Repeat("я", 121), 1),
		"provenance length": strings.Replace(valid, `Собственные изображения; https://example.test`, strings.Repeat("я", 2001), 1),
		"control":           strings.Replace(valid, `Морская тема`, `bad\u0000name`, 1),
		"external asset":    strings.Replace(valid, `"font":`, `"logo":"https://example.test/a.png","font":`, 1),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readFixture(t, zipFixture(t, zipEntry{name: "theme.json", data: []byte(s)}))
			if !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
				t.Fatalf("accepted invalid manifest: %v", err)
			}
		})
	}
}
func TestReadChecksEveryPaletteContrast(t *testing.T) {
	for _, field := range []string{"text", "muted", "danger", "success", "focus", "border"} {
		for _, surface := range []string{"background", "surface", "surface_alt"} {
			t.Run(field+"/"+surface, func(t *testing.T) {
				m := validManifest()
				raw := string(manifestBytes(t, m))
				raw = strings.Replace(raw, `"`+surface+`":"#FFFFFF"`, `"`+surface+`":"#BBBBBB"`, 1)
				color := "#555555"
				if field == "focus" || field == "border" {
					color = "#777777"
				}
				raw = strings.Replace(raw, `"`+field+`":"#000000"`, `"`+field+`":"`+color+`"`, 1)
				_, err := readFixture(t, zipFixture(t, zipEntry{name: "theme.json", data: []byte(raw)}))
				if !errors.Is(err, ErrInvalid) {
					t.Fatal("accepted low contrast")
				}
			})
		}
	}
	m := validManifest()
	m.Palette.AccentText = "#000000"
	_, err := readFixture(t, zipFixture(t, zipEntry{name: "theme.json", data: manifestBytes(t, m)}))
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted low accent contrast")
	}
}
func TestReadRejectsUnsafeArchives(t *testing.T) {
	entry := zipEntry{name: "theme.json", data: manifestBytes(t, validManifest())}
	for _, c := range []struct {
		name    string
		entries []zipEntry
	}{
		{"missing", nil},
		{"unknown", []zipEntry{entry, {name: "script.js", data: []byte("alert(1)")}}},
		{"unused", []zipEntry{entry, {name: "assets/a.png", data: []byte("x")}}},
		{"duplicate", []zipEntry{entry, entry}},
		{"case duplicate", []zipEntry{entry, {name: "THEME.JSON", data: entry.data}}},
		{"dot duplicate", []zipEntry{entry, {name: "./theme.json", data: entry.data}}},
		{"slash alias", []zipEntry{entry, {name: `assets\a.png`, data: []byte("x")}}},
		{"percent alias", []zipEntry{entry, {name: `%74heme.json`, data: entry.data}}},
		{"traversal", []zipEntry{entry, {name: "assets/../evil.png", data: []byte("x")}}},
		{"absolute", []zipEntry{entry, {name: "/theme.json", data: entry.data}}},
		{"drive", []zipEntry{entry, {name: "C:/theme.json", data: entry.data}}},
		{"symlink", []zipEntry{{name: "theme.json", data: entry.data, mode: os.ModeSymlink | 0777}}},
		{"manifest size", []zipEntry{{name: "theme.json", data: append(entry.data, bytes.Repeat([]byte(" "), 64<<10)...)}}},
		{"image size", []zipEntry{entry, {name: "assets/a.png", data: make([]byte, 2<<20+1)}}},
		{"zip bomb", []zipEntry{entry, {name: "assets/a.png", data: make([]byte, 20<<20+1)}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := readFixture(t, zipFixture(t, c.entries...))
			if !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
				t.Fatalf("accepted unsafe archive: %v", err)
			}
		})
	}
	entries := []zipEntry{entry}
	for i := 0; i < 50; i++ {
		entries = append(entries, zipEntry{name: strings.Repeat("d", i+1) + "/", mode: os.ModeDir | 0755})
	}
	if _, err := readFixture(t, zipFixture(t, entries...)); !errors.Is(err, ErrInvalid) {
		t.Fatal("entry limit bypass")
	}
	b := bytes.ReplaceAll(zipFixture(t, entry, zipEntry{name: "assetss", data: []byte("hidden")}), []byte("assetss"), []byte("assets/"))
	if _, err := readFixture(t, b); !errors.Is(err, ErrInvalid) {
		t.Fatal("directory payload accepted")
	}
	b = zipFixture(t, entry)
	cd := bytes.Index(b, []byte("PK\x01\x02"))
	b[cd+16] ^= 1
	if _, err := readFixture(t, b); !errors.Is(err, ErrInvalid) {
		t.Fatal("CRC mismatch accepted")
	}
	b = zipFixture(t, entry)
	cd = bytes.Index(b, []byte("PK\x01\x02"))
	binary.LittleEndian.PutUint32(b[cd+24:], 1)
	if _, err := readFixture(t, b); !errors.Is(err, ErrInvalid) {
		t.Fatal("forged size accepted")
	}
	b = zipFixture(t, entry)
	cd = bytes.Index(b, []byte("PK\x01\x02"))
	binary.LittleEndian.PutUint32(b[cd+24:], 21<<20)
	if _, err := readFixture(t, b); !errors.Is(err, ErrInvalid) {
		t.Fatal("declared limit bypass")
	}
}
func TestReadReferencesAndCancellation(t *testing.T) {
	m := validManifest()
	m.Background = "assets/a.png"
	m.Logo = m.Background
	m.Ornament = "assets/c.webp"
	b := zipFixture(t, zipEntry{name: "theme.json", data: manifestBytes(t, m)}, zipEntry{name: "assets/a.png", data: []byte("png")}, zipEntry{name: "assets/c.webp", data: []byte("webp")})
	a, err := readFixture(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare(context.Background(), nil, a); !errors.Is(err, ErrInvalid) || len(Issues(err)) == 0 {
		t.Fatalf("nil processor images: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Read(ctx, bytes.NewReader(b), int64(len(b))); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation: %v", err)
	}
	if _, err = Prepare(ctx, nil, a); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare cancellation: %v", err)
	}
	if _, err = Read(context.Background(), bytes.NewReader(b), 5<<20+1); !errors.Is(err, ErrInvalid) {
		t.Fatal("source limit bypass")
	}
	if _, err = Read(context.Background(), bytes.NewReader(b), int64(len(b)+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("short source accepted")
	}
	if _, err = Read(context.Background(), nil, int64(len(b))); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil source accepted")
	}
	if _, err = Read(context.Background(), brokenReaderAt{}, 123); !errors.Is(err, ErrInvalid) {
		t.Fatal("source error accepted")
	}
}

type brokenReaderAt struct{}

func (brokenReaderAt) ReadAt([]byte, int64) (int, error) { return 0, io.ErrUnexpectedEOF }

// The caller can expose a larger ReaderAt than this upload. ZIP size fields must
// never make validation inspect bytes outside the declared archive.
func TestReadBoundsCompressedEntriesToDeclaredSource(t *testing.T) {
	b := zipFixture(t, zipEntry{name: "theme.json", data: manifestBytes(t, validManifest())})
	cd := bytes.Index(b, []byte("PK\x01\x02"))
	binary.LittleEndian.PutUint32(b[cd+20:], uint32(len(b)+4096))
	reader := &boundedSource{data: append(append([]byte{}, b...), make([]byte, 8192)...), limit: int64(len(b))}
	_, err := Read(context.Background(), reader, int64(len(b)))
	if reader.outside {
		t.Fatal("ZIP decoder read beyond the declared upload size")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("impossible compressed size accepted")
	}
}

type boundedSource struct {
	data    []byte
	limit   int64
	outside bool
}

func (r *boundedSource) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(b)) > r.limit {
		r.outside = true
	}
	return bytes.NewReader(r.data).ReadAt(b, off)
}
