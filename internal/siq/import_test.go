package siq

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"siplatform/internal/packs"
)

const v5 = `<package name="Тест" version="5.0" language="ru"><info><authors><author>Автор</author></authors><sources><source>https://example.org</source></sources></info><rounds><round name="Раунд"><themes><theme name="Тема"><questions><question price="100" type="simple"><info><showmanComments>Не показывать</showmanComments></info><params><param name="question" type="content"><item type="text">Вопрос?</item><item type="image" isRef="true" duration="00:00:02.500" waitForFinish="true">кот.png</item></param><param name="answer" type="content"><item type="text">Объяснение</item></param></params><right><answer>Ответ</answer></right></question></questions></theme></themes></round></rounds></package>`
const v4 = `<package name="Старый" version="4.0"><rounds><round name="Раунд"><themes><theme name="Тема"><questions><question price="200"><type name="auction"/><scenario><atom>Вопрос</atom><atom type="say">Только ведущему</atom><atom type="voice" time="2.5">@песня.mp3</atom><atom type="marker"/><atom>Пояснение</atom></scenario><right><answer>Ответ</answer></right><wrong><answer>Нет</answer></wrong></question></questions></theme></themes></round></rounds></package>`

type entry struct {
	name string
	data []byte
	mode uint32
}

func archiveBytes(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		if e.mode != 0 {
			h.CreatorVersion = 3 << 8
			h.ExternalAttrs = e.mode << 16
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
	return out.Bytes()
}
func readArchive(t *testing.T, data []byte) *Archive {
	t.Helper()
	a, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func hasCode(issues []packs.Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
func TestImportV5MetadataAndMedia(t *testing.T) {
	media := []byte("media bytes")
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(v5), 0}, entry{"Images/%D0%BA%D0%BE%D1%82.png", media, 0}))
	if !packs.Playable(a.Issues) {
		t.Fatalf("issues: %+v", a.Issues)
	}
	q := a.Document.Rounds[0].Themes[0].Questions[0]
	if a.Document.Info.Authors[0] != "Автор" || q.Info.ShowmanComments != "Не показывать" || q.Content("question")[1].DurationMS != 2500 || q.Content("answer")[0].Value != "Объяснение" {
		t.Fatalf("lost metadata: %+v", a.Document)
	}
	if len(a.Assets) != 1 || a.Assets[0].SHA256 == "" {
		t.Fatal("missing asset hash")
	}
	f, err := a.Assets[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(b, media) {
		t.Fatal("asset changed")
	}
}
func TestImportV4BoundaryAndTypes(t *testing.T) {
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(v4), 0}, entry{"Audio/песня.mp3", []byte("audio"), 0}))
	if !packs.Playable(a.Issues) {
		t.Fatalf("issues: %+v", a.Issues)
	}
	q := a.Document.Rounds[0].Themes[0].Questions[0]
	body := q.Content("question")
	if q.Type != "stake" || len(body) != 3 || body[1].Placement != "replic" || body[2].Type != "audio" || !body[2].IsRef || body[2].DurationMS != 2500 || q.Content("answer")[0].Value != "Пояснение" || q.Wrong[0] != "Нет" {
		t.Fatalf("bad upgrade: %+v", q)
	}
	for old, want := range map[string]string{"simple": "simple", "sponsored": "noRisk", "cat": "secret", "bagcat": "secret"} {
		xml := strings.Replace(v4, `<type name="auction"/>`, `<type name="`+old+`"><param name="theme">Секрет</param><param name="cost">[100;500]/100</param></type>`, 1)
		a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(xml), 0}, entry{"Audio/песня.mp3", []byte("a"), 0}))
		if a.Document.Rounds[0].Themes[0].Questions[0].Type != want {
			t.Fatal(old)
		}
	}
}
func TestImportDiagnostics(t *testing.T) {
	cases := []struct{ name, xml, code string }{
		{"duplicate info", strings.Replace(v5, "<rounds>", "<info><comments>lost</comments></info><rounds>", 1), "duplicate_element"},
		{"namespace", strings.Replace(v5, "<package ", "<package xmlns=\"urn:unknown\" ", 1), "unsupported_namespace"},
		{"parameter type", strings.Replace(v5, `name="question" type="content"`, `name="question" type="program"`, 1), "unsupported_parameter_type"},
		{"missing", v5, "missing_media"},
		{"external", strings.Replace(v5, `isRef="true"`, `isRef="false"`, 1), "external_media"},
		{"unknown type", strings.Replace(v5, `type="simple"`, `type="custom"`, 1), "unsupported_type"},
		{"script", strings.Replace(v5, "<right>", "<script><step/></script><right>", 1), "unsupported_script"},
		{"unknown XML", strings.Replace(v5, "<rounds>", "<custom>secret</custom><rounds>", 1), "unsupported_xml"},
		{"unknown attr", strings.Replace(v5, "<package ", "<package surprise=\"true\" ", 1), "unsupported_attribute"},
		{"unknown version", strings.Replace(v5, `version="5.0"`, `version="9.0"`, 1), "unsupported_version"},
		{"html", strings.Replace(v5, `type="image"`, `type="html"`, 1), "unsupported_content"},
		{"parameter", strings.Replace(v5, `name="question"`, `name="unknown"`, 1), "unsupported_parameter"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(tt.xml), 0}))
			if !hasCode(a.Issues, tt.code) || packs.Playable(a.Issues) {
				t.Fatalf("wanted %s: %+v", tt.code, a.Issues)
			}
		})
	}
}
func TestRejectUnsafeArchives(t *testing.T) {
	cases := []struct {
		name    string
		entries []entry
	}{
		{"traversal", []entry{{"../escape", []byte("x"), 0}}},
		{"absolute", []entry{{"/etc/passwd", []byte("x"), 0}}},
		{"backslash", []entry{{`Images\x.png`, []byte("x"), 0}}},
		{"drive", []entry{{"C:/x", []byte("x"), 0}}},
		{"encoded traversal", []entry{{"Images/%2E%2E/x", []byte("x"), 0}}},
		{"double encoded traversal", []entry{{"Images/%252e%252e/x", []byte("x"), 0}}},
		{"duplicate", []entry{{"content.xml", []byte(v5), 0}, {"content.xml", []byte(v5), 0}}},
		{"case alias", []entry{{"Images/A.png", []byte("x"), 0}, {"Images/a.png", []byte("y"), 0}}},
		{"url alias", []entry{{"Images/a.png", []byte("x"), 0}, {"Images/%61.png", []byte("y"), 0}}},
		{"unicode alias", []entry{{"Images/é.png", []byte("x"), 0}, {"Images/é.png", []byte("y"), 0}}},
		{"symlink", []entry{{"Images/link", []byte("/etc/passwd"), 0120777}}},
		{"nested archive", []entry{{"inner.zip", []byte("PK\x03\x04"), 0}}},
		{"missing content", []entry{{"Images/a.png", []byte("x"), 0}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			data := archiveBytes(t, tt.entries...)
			if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)), DefaultLimits()); err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
}
func TestRejectUnsafeXML(t *testing.T) {
	cases := []string{`<!DOCTYPE package [<!ENTITY leak SYSTEM "file:///etc/passwd">]>` + v5, v5 + v5, `<?run command?>` + v5, strings.Replace(v5, "Вопрос?", "&missing;", 1), `<package>` + strings.Repeat("<a>", 65) + strings.Repeat("</a>", 65) + `</package>`, strings.Replace(v5, `duration="00:00:02.500"`, `duration="NaN"`, 1)}
	for i, s := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			data := archiveBytes(t, entry{"content.xml", []byte(s), 0})
			a, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)), DefaultLimits())
			if err == nil && packs.Playable(a.Issues) {
				t.Fatal("unsafe XML accepted")
			}
		})
	}
}
func TestArchiveLimitsCRCAndCancellation(t *testing.T) {
	data := archiveBytes(t, entry{"content.xml", []byte(v5), 0}, entry{"Images/кот.png", []byte("payload"), 0})
	for name, change := range map[string]func(*Limits){"archive": func(l *Limits) { l.ArchiveBytes = 8 }, "unpacked": func(l *Limits) { l.UnpackedBytes = 8 }, "xml": func(l *Limits) { l.XMLBytes = 8 }, "files": func(l *Limits) { l.Files = 1 }, "media": func(l *Limits) { l.MediaBytes = 2 }, "nodes": func(l *Limits) { l.XMLNodes = 5 }} {
		t.Run(name, func(t *testing.T) {
			l := DefaultLimits()
			change(&l)
			if _, err := Read(context.Background(), bytes.NewReader(data), int64(len(data)), l); err == nil {
				t.Fatal("limit ignored")
			}
		})
	}
	corrupt := append([]byte(nil), data...)
	at := bytes.Index(corrupt, []byte("payload"))
	corrupt[at] = 'X'
	if _, err := Read(context.Background(), bytes.NewReader(corrupt), int64(len(corrupt)), DefaultLimits()); err == nil {
		t.Fatal("CRC ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, bytes.NewReader(data), int64(len(data)), DefaultLimits()); err == nil {
		t.Fatal("cancel ignored")
	}
	bad := append([]byte(nil), data...)
	eocd := bytes.LastIndex(bad, []byte("PK\x05\x06"))
	binary.LittleEndian.PutUint16(bad[eocd+10:], 6000)
	if _, err := Read(context.Background(), bytes.NewReader(bad), int64(len(bad)), DefaultLimits()); err == nil {
		t.Fatal("directory count ignored")
	}
}
