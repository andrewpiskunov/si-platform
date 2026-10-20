package siq

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/packs"
)

type measuredReader struct {
	reader *bytes.Reader
	bytes  int
	cancel context.CancelFunc
}

func (r *measuredReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.reader.ReadAt(p, off)
	r.bytes += n
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return n, err
}
func TestForgedDirectoryBoundBeforeZIPAllocation(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 20000; i++ {
		if _, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("f%05d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	end := bytes.LastIndex(data, []byte("PK\x05\x06"))
	binary.LittleEndian.PutUint16(data[end+8:], 1)
	binary.LittleEndian.PutUint16(data[end+10:], 1)
	binary.LittleEndian.PutUint32(data[end+12:], 1)
	r := &measuredReader{reader: bytes.NewReader(data)}
	l := DefaultLimits()
	l.Files = 2
	if _, err := Read(context.Background(), r, int64(len(data)), l); err == nil {
		t.Fatal("forged directory accepted")
	}
	if r.bytes > 128<<10 {
		t.Errorf("scanned/allocated actual directory after forged preflight: read %d bytes", r.bytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r = &measuredReader{reader: bytes.NewReader(data), cancel: cancel}
	if _, err := Read(ctx, r, int64(len(data)), l); !errors.Is(err, context.Canceled) {
		t.Fatalf("preflight cancellation ignored: %v", err)
	}
}
func TestLegacyTimingAndFinalDefault(t *testing.T) {
	for _, tt := range []struct {
		time string
		wait bool
	}{{"", true}, {` time="0"`, true}, {` time="-1"`, false}, {` time="2.5"`, true}} {
		fixture := strings.Replace(v4, ` time="2.5"`, tt.time, 1)
		fixture = strings.Replace(fixture, `<type name="auction"/>`, `<type name="simple"/>`, 1)
		fixture = strings.Replace(fixture, `<round name="Раунд">`, `<round name="Раунд" type="final">`, 1)
		a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Audio/песня.mp3", []byte("audio"), 0}))
		q := a.Document.Rounds[0].Themes[0].Questions[0]
		if q.Type != "" || q.Content("question")[2].WaitForFinish != tt.wait {
			t.Errorf("time %q: type %q, content %+v", tt.time, q.Type, q.Content("question")[2])
		}
	}
}
func TestForeignAttributesAndStructuralTextDiagnosed(t *testing.T) {
	for _, fixture := range []string{strings.Replace(v5, `price="100"`, `xmlns:x="urn:foreign" x:price="100"`, 1), strings.Replace(v5, "<rounds>", "LOST TEXT<rounds>", 1)} {
		a, err := Read(context.Background(), bytes.NewReader(archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0})), int64(len(archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0}))), DefaultLimits())
		if err == nil && packs.Playable(a.Issues) {
			t.Error("unknown XML silently accepted")
		}
	}
}
func TestLogoReferences(t *testing.T) {
	for _, tt := range []struct {
		logo string
		ok   bool
	}{{"@кот.png", true}, {"@missing.png", false}, {"https://example.org/logo.png", false}} {
		fixture := strings.Replace(v5, "<package ", `<package logo="`+tt.logo+`" `, 1)
		a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0}))
		if packs.Playable(a.Issues) != tt.ok {
			t.Fatalf("logo %q: %+v", tt.logo, a.Issues)
		}
	}
}
func TestGlobalMetadataNormalizationAndShape(t *testing.T) {
	fixture := strings.Replace(v5, "<rounds>", "<global/><rounds>", 1)
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0}))
	if a.Document.GlobalXML != "" {
		t.Error("empty global metadata must normalize to absent, matching official serializer")
	}
	// This structure is emitted by actual SIPackages; its current XSD describes a different global shape.
	valid := `<global><Authors><Author id="a1"><Name>Автор</Name><SecondName/><Surname/><Country/><City/></Author></Authors></global>`
	a.Document.GlobalXML = valid
	var out bytes.Buffer
	if err := Write(context.Background(), &out, a.Document, a.Assets); err != nil {
		t.Fatal(err)
	}
	b := readArchive(t, out.Bytes())
	if !strings.Contains(b.Document.GlobalXML, "a1") {
		t.Fatal("global metadata lost")
	}
	a.Document.GlobalXML = `<global><Authors><Author><Name>Incomplete</Name></Author></Authors></global>`
	if err := Write(context.Background(), io.Discard, a.Document, a.Assets); err == nil {
		t.Fatal("invalid author structure exported")
	}
}
func TestExportEntryPolicyAndInvalidXMLStrings(t *testing.T) {
	for _, value := range []string{"nul\x00name", string([]byte{0xff}), "\ufffename"} {
		d := examples.Demo()
		d.Name = value
		if err := Write(context.Background(), io.Discard, d, nil); err == nil {
			t.Errorf("invalid XML string silently changed: %q", value)
		}
	}
	for _, name := range []string{"extra.zip", "extra.siq", "extra.tar"} {
		a := Asset{Name: name, Size: 1, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("x")), nil }}
		if err := Write(context.Background(), io.Discard, examples.Demo(), []Asset{a}); err == nil {
			t.Error("exported an asset forbidden by import:", name)
		}
	}
}
func TestParameterPaddingShapeAndFileManifest(t *testing.T) {
	fixture := strings.Replace(v5, "<rounds>", `<files><file name="Images/кот.png" hash="kept-as-metadata"/></files><rounds>`, 1)
	fixture = strings.Replace(fixture, `</params>`, `<param name="theme" type="simple">  meaningful padding  </param></params>`, 1)
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(fixture), 0}, entry{"Images/кот.png", []byte("image"), 0}))
	q := a.Document.Rounds[0].Themes[0].Questions[0]
	if q.Params[2].Value != "  meaningful padding  " {
		t.Error("parameter whitespace lost")
	}
	var out bytes.Buffer
	if err := Write(context.Background(), &out, a.Document, a.Assets); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "content.xml" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("kept-as-metadata")) {
				t.Error("file manifest dropped")
			}
		}
	}
	q.Params[0].Type = "simple"
	a.Document.Rounds[0].Themes[0].Questions[0] = q
	if packs.Playable(packs.Validate(a.Document)) {
		t.Fatal("mismatched parameter type/payload accepted")
	}
}
