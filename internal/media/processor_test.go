package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These boundaries must reject before attempting a subprocess or consuming data.
func TestPrepareRejectsInvalidInputs(t *testing.T) {
	p := &Processor{}
	cases := []struct {
		kind string
		size int64
		code string
	}{
		{"html", 1, "unsupported"}, {"image", 0, "invalid"}, {"image", -1, "invalid"}, {"audio", 104857601, "too_large"},
	}
	for _, c := range cases {
		_, err := p.Prepare(context.Background(), c.kind, strings.NewReader("x"), c.size)
		if Code(err) != c.code {
			t.Errorf("%s/%d: got %v (%s), want %s", c.kind, c.size, err, Code(err), c.code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Prepare(ctx, "image", strings.NewReader("x"), 1); Code(err) != "canceled" {
		t.Errorf("cancel: %v", err)
	}
	if _, err := p.Prepare(context.Background(), "image", nil, 1); Code(err) != "invalid" {
		t.Errorf("nil: %v", err)
	}
}

func TestSpoolChecksActualLengthAndCleansFailure(t *testing.T) {
	for _, c := range []struct {
		body string
		size int64
		code string
	}{{"abc", 2, "invalid"}, {"a", 2, "invalid"}, {"abc", 3, ""}} {
		dir := t.TempDir()
		err := copySource(context.Background(), filepath.Join(dir, "source"), strings.NewReader(c.body), c.size)
		if Code(err) != c.code {
			t.Errorf("%q/%d: got %v", c.body, c.size, err)
		}
		if err == nil {
			got, _ := os.ReadFile(filepath.Join(dir, "source"))
			if string(got) != c.body {
				t.Fatal("source changed")
			}
		}
	}
}

func TestBoundedWriterStopsProducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &boundedBuffer{limit: 4, cancel: cancel}
	n, err := w.Write([]byte("secret payload"))
	if n != 4 || err == nil || len(w.Bytes()) != 4 || ctx.Err() == nil {
		t.Fatalf("not bounded: n=%d err=%v data=%q", n, err, w.Bytes())
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("leaked diagnostic")
	}
}

func TestResultCloseOnlyRemovesOwnedDirectory(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "owned")
	outside := filepath.Join(root, "outside")
	os.Mkdir(owned, 0700)
	os.WriteFile(outside, []byte("keep"), 0600)
	r := &Result{directory: owned, Files: []Output{{Path: outside}}}
	r.Close()
	r.Close()
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned directory survived")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("deleted foreign file")
	}
}

func TestErrorsDoNotExposeDiagnostics(t *testing.T) {
	for _, err := range []error{mediaError("invalid"), mediaError("unavailable"), errors.New("/secret/path confidential stderr")} {
		code := Code(err)
		if code != "invalid" && code != "unavailable" {
			t.Fatalf("unsafe code %q", code)
		}
	}
	if Code(nil) != "" {
		t.Fatal("nil should have no code")
	}
}

func TestProbeRejectsUnsafeMetadata(t *testing.T) {
	good := `{"streams":[{"codec_type":"video","codec_name":"h264","width":320,"height":240}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"1.25"}}`
	cases := []struct{ name, json, kind, code string }{
		{"valid", good, "video", ""},
		{"nan", strings.Replace(good, "1.25", "NaN", 1), "video", "invalid"},
		{"infinite", strings.Replace(good, "1.25", "Inf", 1), "video", "invalid"},
		{"overflow", strings.Replace(good, "1.25", "1e100", 1), "video", "too_long"},
		{"negative", strings.Replace(good, "1.25", "-1", 1), "video", "invalid"},
		{"long", strings.Replace(good, "1.25", "600.001", 1), "video", "too_long"},
		{"dimensions", strings.Replace(good, "320", "3841", 1), "video", "too_large"},
		{"kind", good, "audio", "unsupported"},
		{"playlist", strings.Replace(good, "mov,mp4,m4a,3gp,3g2,mj2", "hls", 1), "video", "unsupported"},
		{"malformed", `{"streams":`, "video", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseProbe([]byte(c.json), c.kind)
			if Code(err) != c.code {
				t.Fatalf("got %v (%s), want %s", err, Code(err), c.code)
			}
		})
	}
}

func TestImageRejectsActiveAndAnimatedContent(t *testing.T) {
	for _, content := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), []byte("GIF89a"), []byte("#EXTM3U\nhttp://127.0.0.1/secret")} {
		if err := checkImage(bytes.NewReader(content)); Code(err) != "unsupported" {
			t.Fatalf("accepted %q: %v", content, err)
		}
	}
}

var _ io.Reader = (*bytes.Reader)(nil)

// os/exec uses io.Copy, so embedding bytes.Buffer must not bypass the bound via ReaderFrom.
func TestBoundedWriterThroughIOCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &boundedBuffer{limit: 4, cancel: cancel}
	_, err := io.Copy(w, io.LimitReader(strings.NewReader("too many bytes"), 14))
	if err == nil || w.Len() != 4 || ctx.Err() == nil {
		t.Fatalf("io.Copy bypassed bound: %v len=%d", err, w.Len())
	}
}

func TestOutputVerificationRejectsTruncatedShortClip(t *testing.T) {
	source := metadata{durationMS: 200}
	out := metadata{durationMS: 1, Streams: []probeStream{{Type: "audio", Codec: "aac"}}}
	out.Format.Name = "mov,mp4,m4a,3gp,3g2,mj2"
	if err := validateOutput("audio", source, out, false); Code(err) != "invalid" {
		t.Fatalf("truncated clip accepted: %v", err)
	}
}

func TestCopySourceStopsAtDeclaredSizePlusOne(t *testing.T) {
	source := &countingSource{}
	if err := copySource(context.Background(), filepath.Join(t.TempDir(), "source"), source, 10); Code(err) != "invalid" {
		t.Fatalf("oversized stream accepted: %v", err)
	}
	if source.count != 11 {
		t.Fatalf("consumed beyond bound: %d", source.count)
	}
}

type countingSource struct{ count int }

func (r *countingSource) Read(p []byte) (int, error) { r.count += len(p); return len(p), nil }

func TestNewFailsClosedForMissingSandbox(t *testing.T) {
	if p, err := New(Options{Sandbox: filepath.Join(t.TempDir(), "absent")}); p != nil || Code(err) != "unavailable" {
		t.Fatalf("missing sandbox accepted: %+v %v", p, err)
	}
}
