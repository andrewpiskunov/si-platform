//go:build linux

package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func integrationOptions(t *testing.T) Options {
	t.Helper()
	if os.Getenv("MEDIA_INTEGRATION") != "1" {
		t.Skip("set MEDIA_INTEGRATION=1 to require real FFmpeg/bubblewrap integration")
	}
	return Options{FFmpeg: os.Getenv("MEDIA_FFMPEG"), FFprobe: os.Getenv("MEDIA_FFPROBE"), Sandbox: "/usr/bin/bwrap", Prlimit: "/usr/bin/prlimit", Directory: t.TempDir()}
}
func integrationProcessor(t *testing.T) *Processor {
	t.Helper()
	p, err := New(integrationOptions(t))
	if err != nil {
		t.Fatalf("real sandbox unavailable: %v", err)
	}
	return p
}
func fixturePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 27, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func fixtureWAV(seconds int) []byte {
	data := make([]byte, 44+seconds*8000*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}
func ffmpegFixture(t *testing.T, p *Processor, args ...string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	argv := append([]string{"-v", "error", "-nostdin", "-threads", "2", "-filter_threads", "1"}, args...)
	argv = append(argv, path)
	cmd := exec.Command(p.options.FFmpeg, argv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("trusted fixture: %v %s", err, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIntegrationRealPreparation(t *testing.T) {
	p := integrationProcessor(t)
	fixtures := []struct {
		name, kind, mime string
		data             []byte
		width, height    int
		duration         int64
	}{
		{"PNG", "image", "image/png", fixturePNG(t, 1800, 100), 1800, 100, 0},
		{"WAV", "audio", "audio/mp4", fixtureWAV(1), 0, 0, 1000},
		{"MP4", "video", "video/mp4", ffmpegFixture(t, p, "-f", "lavfi", "-i", "color=c=red:s=1280x720:r=10:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-threads", "2", "-c:a", "aac", "-f", "mp4"), 1280, 720, 1000},
		{"WebP", "image", "image/png", ffmpegFixture(t, p, "-f", "lavfi", "-i", "color=c=blue:s=64x32", "-frames:v", "1", "-c:v", "libwebp", "-f", "webp"), 64, 32, 0},
	}
	for _, c := range fixtures {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Prepare(context.Background(), c.kind, bytes.NewReader(c.data), int64(len(c.data)))
			if err != nil {
				t.Fatalf("real prepare failed: %v", err)
			}
			defer result.Close()
			if result.Prepared.Kind != c.kind || result.Prepared.Width != c.width || result.Prepared.Height != c.height || abs(result.Prepared.DurationMS-c.duration) > 150 {
				t.Fatalf("metadata: %+v", result.Prepared)
			}
			if len(result.Files) != 2 {
				t.Fatalf("outputs: %+v", result.Files)
			}
			for _, out := range result.Files {
				b, err := os.ReadFile(out.Path)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(b)
				if out.Key != hex.EncodeToString(sum[:]) || out.Size != int64(len(b)) || out.MIME != c.mime {
					t.Fatalf("unverified output: %+v", out)
				}
				if out.Size <= 0 || out.Size > 104857600 {
					t.Fatalf("size: %d", out.Size)
				}
			}
			if c.name == "MP4" {
				for i, out := range result.Files {
					b, err := exec.Command(p.options.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", out.Path).Output()
					want := "1280,720"
					if i == 1 {
						want = "640,360"
					}
					if err != nil || strings.TrimSpace(string(b)) != want {
						t.Fatalf("video dimensions: %q want %s: %v", b, want, err)
					}
				}
			}
			if c.name == "PNG" {
				f, _ := os.Open(result.Files[1].Path)
				defer f.Close()
				cfg, _, err := image.DecodeConfig(f)
				if err != nil || cfg.Width != 1600 || cfg.Height > 100 {
					t.Fatalf("economy dimensions: %+v %v", cfg, err)
				}
			}
			owned := result.directory
			result.Close()
			if _, err := os.Stat(owned); !os.IsNotExist(err) {
				t.Fatal("result not cleaned")
			}
		})
	}
}

func TestIntegrationRejectsInvalidMediaAndCleans(t *testing.T) {
	p := integrationProcessor(t)
	valid := fixturePNG(t, 32, 32)
	cases := []struct {
		name, kind, code string
		data             []byte
	}{
		{"html", "image", "unsupported", []byte("<html>script</html>")},
		{"corruptPNG", "image", "invalid", valid[:len(valid)/2]},
		{"truncatedWAV", "audio", "invalid", fixtureWAV(1)[:8000]},
		{"duration", "audio", "too_long", fixtureWAV(601)},
		{"playlist", "video", "invalid", []byte("#EXTM3U\nhttp://127.0.0.1/private")},
		{"localReference", "video", "invalid", []byte("ffconcat version 1.0\nfile '/etc/passwd'\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := p.Prepare(context.Background(), c.kind, bytes.NewReader(c.data), int64(len(c.data)))
			if r != nil {
				r.Close()
				t.Fatal("partial result accepted")
			}
			if Code(err) != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
			entries, _ := os.ReadDir(p.options.Directory)
			if len(entries) != 0 {
				t.Fatalf("left temporary files: %+v", entries)
			}
		})
	}
}

func TestIntegrationSandboxLimitsAndIsolation(t *testing.T) {
	p := integrationProcessor(t)
	var err error
	p.runtime, err = runtimeFiles(p.options.FFmpeg, p.options.FFprobe, p.options.Prlimit, "/usr/bin/cat", "/usr/bin/env", "/usr/bin/dd", "/usr/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	in := t.TempDir()
	out := t.TempDir()
	// The real sandbox must exclude host files even when protocol restrictions are removed.
	if _, err := p.run(context.Background(), "/usr/bin/cat", in, out, []string{"/etc/passwd"}); err == nil {
		t.Fatal("host file was readable")
	}
	t.Setenv("MEDIA_TEST_SECRET", "must-not-reach-transcoder")
	env, err := p.run(context.Background(), "/usr/bin/env", in, out, nil)
	if err != nil || string(env) != "MALLOC_ARENA_MAX=2\n" {
		t.Fatalf("sandbox environment: %q %v", env, err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(fixtureWAV(1)) }))
	defer server.Close()
	_, err = p.run(context.Background(), p.options.FFprobe, in, out, []string{"-v", "error", "-protocol_whitelist", "http,tcp", "-i", server.URL})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("network escaped: %v calls=%d", err, calls.Load())
	}
	// Timeout must kill and reap the process, not return a partial result.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = p.run(ctx, "/usr/bin/sleep", in, out, []string{"60"})
	if Code(err) != "timeout" || time.Since(start) > 2*time.Second {
		t.Fatalf("deadline not enforced: %v, %v", err, time.Since(start))
	}
	// stdout is bounded independently of file size. A real producer must stop.
	_, err = p.run(context.Background(), "/usr/bin/cat", in, out, []string{"/dev/zero"})
	if Code(err) != "too_large" {
		t.Fatalf("unbounded output: %v", err)
	}
	_, err = p.run(context.Background(), "/usr/bin/dd", in, out, []string{"if=/dev/zero", "of=/output/huge", "bs=1048576", "count=101"})
	st, statErr := os.Stat(filepath.Join(out, "huge"))
	if err == nil || statErr != nil || st.Size() > 104857600 {
		t.Fatalf("file limit failed: %v %v %+v", err, statErr, st)
	}
}

func TestIntegrationTimeoutAndCancellationCleanWorkingDirectory(t *testing.T) {
	p := integrationProcessor(t)
	p.timeout = time.Nanosecond
	if r, err := p.Prepare(context.Background(), "image", bytes.NewReader(fixturePNG(t, 32, 32)), 100); r != nil || Code(err) != "timeout" {
		t.Fatalf("timeout: %v %+v", err, r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Prepare(ctx, "image", strings.NewReader("x"), 1); Code(err) != "canceled" {
		t.Fatalf("cancel: %v", err)
	}
	entries, _ := os.ReadDir(p.options.Directory)
	if len(entries) != 0 {
		t.Fatal("temporary files leaked")
	}
}
func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestIntegrationAnimatedAndOversize(t *testing.T) {
	p := integrationProcessor(t)
	animated := ffmpegFixture(t, p, "-f", "lavfi", "-i", "testsrc=size=32x32:rate=2:duration=1", "-plays", "0", "-f", "apng")
	webp := ffmpegFixture(t, p, "-f", "lavfi", "-i", "testsrc=size=32x32:rate=2:duration=1", "-c:v", "libwebp_anim", "-loop", "0", "-f", "webp")
	bigImage := fixturePNG(t, 4001, 4000)
	bigVideo := ffmpegFixture(t, p, "-f", "lavfi", "-i", "color=c=red:s=3842x16:r=1:d=1", "-c:v", "libx264", "-threads", "1", "-f", "mp4")
	cases := []struct {
		name, kind, code string
		data             []byte
	}{{"APNG", "image", "unsupported", animated}, {"animatedWebP", "image", "unsupported", webp}, {"16MP", "image", "too_large", bigImage}, {"4K", "video", "too_large", bigVideo}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Prepare(context.Background(), c.kind, bytes.NewReader(c.data), int64(len(c.data)))
			if result != nil {
				result.Close()
			}
			if Code(err) != c.code {
				t.Fatalf("got %v want %s", err, c.code)
			}
		})
	}
}

func TestIntegrationCancellationStopsBlockedSourceAndQueuedThirdJob(t *testing.T) {
	p := integrationProcessor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 3)
	readers := make([]*signaledReader, 3)
	for i := 0; i < 2; i++ {
		r, w := io.Pipe()
		defer w.Close()
		readers[i] = &signaledReader{PipeReader: r, started: make(chan struct{}, 1)}
		go func(source io.Reader) { _, err := p.Prepare(ctx, "audio", source, 1); errs <- err }(readers[i])
		select {
		case <-readers[i].started:
		case <-time.After(2 * time.Second):
			t.Fatal("reader did not start")
		}
	}
	r, w := io.Pipe()
	defer w.Close()
	readers[2] = &signaledReader{PipeReader: r, started: make(chan struct{}, 1)}
	third, stopThird := context.WithCancel(context.Background())
	go func() { _, err := p.Prepare(third, "audio", readers[2], 1); errs <- err }()
	stopThird()
	select {
	case err := <-errs:
		if Code(err) != "canceled" {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued job ignored cancellation")
	}
	select {
	case <-readers[2].started:
		t.Fatal("third reader started beyond concurrency bound")
	default:
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if Code(err) != "canceled" {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("blocked closable reader leaked")
		}
	}
	entries, _ := os.ReadDir(p.options.Directory)
	if len(entries) != 0 {
		t.Fatal("cancellation leaked temporary files")
	}
}

type signaledReader struct {
	*io.PipeReader
	started chan struct{}
}

func (r *signaledReader) Read(p []byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	return r.PipeReader.Read(p)
}

func TestIntegrationSandboxMemoryAndProcessGroup(t *testing.T) {
	p := integrationProcessor(t)
	// This trusted fixture forks a descendant so killing only the immediate
	// executable would keep changing the marker after run returned.
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.c")
	binaryPath := filepath.Join(dir, "fixture")
	code := `#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <sys/resource.h>
int main(int argc,char **argv) {
 struct rlimit m,c; getrlimit(RLIMIT_AS,&m);getrlimit(RLIMIT_CPU,&c);
 if(m.rlim_cur!=536870912||m.rlim_max!=536870912||c.rlim_cur!=180||c.rlim_max!=180)return 10;
 if(argc>1) {void *volatile p=malloc(536870912);if(p){free(p);return 11;}return 0;}
 if(fork()==0){FILE *f=fopen("/output/marker","w");if(!f)return 12;for(;;){fputs("x",f);fflush(f);usleep(10000);}}
 for(;;)pause();
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	if out, err := exec.Command(cc, "-O2", source, "-o", binaryPath).CombinedOutput(); err != nil {
		t.Fatalf("compile sandbox fixture: %v %s", err, out)
	}
	runtime, err := runtimeFiles(p.options.FFmpeg, p.options.FFprobe, p.options.Prlimit, p.envTool, binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	p.runtime = runtime
	input, output := t.TempDir(), t.TempDir()
	if _, err := p.run(context.Background(), binaryPath, input, output, []string{"allocate"}); err != nil {
		t.Fatalf("memory/CPU limit not applied: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.run(ctx, binaryPath, input, output, nil); done <- err }()
	marker := filepath.Join(output, "marker")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if st, err := os.Stat(marker); err == nil && st.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("child never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if Code(err) != "canceled" {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process group was not reaped")
	}
	st, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	after, err := os.Stat(marker)
	if err != nil || after.Size() != st.Size() {
		t.Fatal("descendant survived cancellation")
	}
}
