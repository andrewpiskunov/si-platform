package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxBytes      int64 = 100 << 20
	maxDuration         = 600 * time.Second
	maxProcessing       = 180 * time.Second
)

type mediaError string

func (e mediaError) Error() string { return "media: " + string(e) }

// Code is safe to expose to a client; subprocess paths and diagnostics are private.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var e mediaError
	if errors.As(err, &e) {
		return string(e)
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "invalid"
}

// Close releases only this result's private working directory. It is idempotent.
func (r *Result) Close() error {
	if r == nil || r.directory == "" {
		return nil
	}
	return os.RemoveAll(r.directory)
}

type Processor struct {
	options Options
	slots   chan struct{}
	runtime []runtimeFile
	envTool string
	timeout time.Duration
}

type runtimeFile struct{ source, target string }

func (p *Processor) Prepare(ctx context.Context, kind string, source io.Reader, size int64) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, mediaError(Code(err))
	}
	if kind != "image" && kind != "audio" && kind != "video" {
		return nil, mediaError("unsupported")
	}
	if size > maxBytes {
		return nil, mediaError("too_large")
	}
	if size <= 0 || source == nil {
		return nil, mediaError("invalid")
	}
	if p.slots == nil {
		return nil, mediaError("unavailable")
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return nil, mediaError(Code(ctx.Err()))
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = maxProcessing
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return nil, mediaError(Code(ctx.Err()))
	}
	directory, err := os.MkdirTemp(p.options.Directory, "media-")
	if err != nil {
		return nil, mediaError("unavailable")
	}
	result := &Result{directory: directory}
	successful := false
	defer func() {
		if !successful {
			_ = result.Close()
		}
	}()
	input, output := filepath.Join(directory, "input"), filepath.Join(directory, "output")
	if os.Mkdir(input, 0700) != nil || os.Mkdir(output, 0700) != nil {
		return nil, mediaError("unavailable")
	}
	sourcePath := filepath.Join(input, "source")
	if err = copySource(ctx, sourcePath, source, size); err != nil {
		return nil, err
	}
	if kind == "image" {
		f, err := os.Open(sourcePath)
		if err != nil {
			return nil, mediaError("unavailable")
		}
		err = checkImage(f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	meta, err := p.probe(ctx, input, output, "/input/source", kind)
	if err != nil {
		return nil, err
	}
	if err = p.decode(ctx, input, output, "/input/source", kind, meta); err != nil {
		return nil, err
	}
	result.Prepared = Prepared{Kind: kind, DurationMS: meta.durationMS, Width: meta.width, Height: meta.height}
	for _, economy := range []bool{false, true} {
		name := "standard"
		if economy {
			name = "economy"
		}
		mime, extension := "image/png", ".png"
		if kind == "audio" {
			mime, extension = "audio/mp4", ".m4a"
		}
		if kind == "video" {
			mime, extension = "video/mp4", ".mp4"
		}
		localPath := filepath.Join(output, name+extension)
		sandboxPath := "/output/" + name + extension
		args := p.transcodeArgs(kind, meta, economy, sandboxPath)
		if _, err = p.run(ctx, p.options.FFmpeg, input, output, args); err != nil {
			if st, statErr := os.Stat(localPath); statErr == nil && st.Size() >= maxBytes {
				return nil, mediaError("too_large")
			}
			return nil, err
		}
		outMeta, err := p.probe(ctx, input, output, sandboxPath, kind)
		if err != nil {
			return nil, err
		}
		if err = validateOutput(kind, meta, outMeta, economy); err != nil {
			return nil, err
		}
		if err = p.decode(ctx, input, output, sandboxPath, kind, outMeta); err != nil {
			return nil, err
		}
		f, err := os.Open(localPath)
		if err != nil {
			return nil, mediaError("invalid")
		}
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 {
			f.Close()
			return nil, mediaError("invalid")
		}
		if stat.Size() > maxBytes {
			f.Close()
			return nil, mediaError("too_large")
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(contextReader{ctx, f}, maxBytes+1))
		f.Close()
		if ctx.Err() != nil {
			return nil, mediaError(Code(ctx.Err()))
		}
		if err != nil || n != stat.Size() {
			return nil, mediaError("invalid")
		}
		variant := Variant{Key: hex.EncodeToString(hash.Sum(nil)), MIME: mime, Size: n}
		if economy {
			result.Prepared.Economy = variant
		} else {
			result.Prepared.Standard = variant
		}
		result.Files = append(result.Files, Output{Key: variant.Key, Path: localPath, MIME: mime, Size: n})
	}
	if ctx.Err() != nil {
		return nil, mediaError(Code(ctx.Err()))
	}
	successful = true
	return result, nil
}

func copySource(ctx context.Context, path string, source io.Reader, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return mediaError("unavailable")
	}
	defer f.Close()
	// A closable source can be interrupted while blocked in Read. For a plain
	// Reader cancellation is checked between reads; callers must supply a
	// context-aware Reader if its Read can block indefinitely.
	if closer, ok := source.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stop()
	}
	n, err := io.Copy(f, io.LimitReader(contextReader{ctx, source}, size+1))
	if ctx.Err() != nil {
		return mediaError(Code(ctx.Err()))
	}
	if n > maxBytes {
		return mediaError("too_large")
	}
	if err != nil || n != size {
		return mediaError("invalid")
	}
	if err = f.Close(); err != nil {
		return mediaError("unavailable")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remain := b.limit - b.Len()
	if len(p) > remain {
		n, _ := b.buffer.Write(p[:remain])
		b.exceeded = true
		b.cancel()
		return n, mediaError("too_large")
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *boundedBuffer) Len() int      { return b.buffer.Len() }

func (p *Processor) probe(ctx context.Context, input, output, path, kind string) (metadata, error) {
	args := []string{"-v", "error", "-max_alloc", "67108864", "-threads", "1", "-probesize", "5242880", "-analyzeduration", "5000000", "-protocol_whitelist", "file", "-format_whitelist", demuxers, "-show_entries", "format=format_name,duration:stream=codec_type,codec_name,width,height,duration,nb_frames,sample_rate,channels", "-of", "json", path}
	b, err := p.run(ctx, p.options.FFprobe, input, output, args)
	if err != nil {
		return metadata{}, err
	}
	return parseProbe(b, kind)
}

func inputArgs(path string) []string {
	return []string{"-hide_banner", "-v", "error", "-nostdin", "-y", "-xerror", "-max_error_rate", "0", "-max_alloc", "67108864", "-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1", "-err_detect", "explode", "-probesize", "5242880", "-analyzeduration", "5000000", "-protocol_whitelist", "file", "-format_whitelist", demuxers, "-i", path}
}

func (p *Processor) decode(ctx context.Context, input, output, path, kind string, meta metadata) error {
	args := inputArgs(path)
	args = append(args, "-map", "0", "-threads", "1", "-progress", "pipe:1", "-nostats", "-f", "null", "/dev/null")
	b, err := p.run(ctx, p.options.FFmpeg, input, output, args)
	if err != nil {
		return err
	}
	var duration, frames int64
	ended := false
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "progress":
			if value == "end" {
				ended = true
			}
		case "out_time_us":
			if value == "N/A" {
				continue
			}
			v, e := strconv.ParseInt(value, 10, 64)
			if e != nil {
				return mediaError("invalid")
			}
			if v > duration {
				duration = v
			}
		case "frame":
			v, e := strconv.ParseInt(value, 10, 64)
			if e != nil {
				return mediaError("invalid")
			}
			if v > frames {
				frames = v
			}
		}
	}
	if !ended {
		return mediaError("invalid")
	}
	if kind == "image" {
		if frames != 1 {
			return mediaError("unsupported")
		}
		return nil
	}
	if duration > int64(maxDuration/time.Microsecond) {
		return mediaError("too_long")
	}
	if duration <= 0 || difference(duration/1000, meta.durationMS) > durationTolerance(meta.durationMS) {
		return mediaError("invalid")
	}
	return nil
}

func (p *Processor) transcodeArgs(kind string, meta metadata, economy bool, path string) []string {
	args := inputArgs("/input/source")
	args = append(args, "-map_metadata", "-1", "-map_chapters", "-1")
	switch kind {
	case "image":
		args = append(args, "-map", "0:v:0", "-an")
		if economy {
			args = append(args, "-vf", "scale=w='min(iw,1600)':h='min(ih,1600)':force_original_aspect_ratio=decrease")
		}
		args = append(args, "-c:v", "png", "-threads", "1", "-f", "image2", "-update", "1")
	case "audio":
		bitrate := "128k"
		if economy {
			bitrate = "64k"
		}
		args = append(args, "-map", "0:a:0", "-vn", "-c:a", "aac", "-b:a", bitrate, "-ac", "2", "-ar", "48000", "-threads", "1", "-movflags", "+faststart", "-f", "mp4")
	case "video":
		width, height, crf := 1920, 1080, "23"
		if economy {
			width, height, crf = 640, 360, "28"
		}
		bitrate := "128k"
		if economy {
			bitrate = "64k"
		}
		filter := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2,setsar=1", width, height)
		args = append(args, "-map", "0:v:0", "-map", "0:a:0?", "-vf", filter, "-c:v", "libx264", "-preset", "veryfast", "-crf", crf, "-pix_fmt", "yuv420p", "-threads", "1", "-c:a", "aac", "-b:a", bitrate, "-ac", "2", "-ar", "48000", "-movflags", "+faststart", "-f", "mp4")
	}
	return append(args, path)
}

func validateOutput(kind string, source, out metadata, economy bool) error {
	for _, stream := range out.Streams {
		if (stream.Type == "audio" && stream.Codec != "aac") || (stream.Type == "video" && ((kind == "image" && stream.Codec != "png") || (kind == "video" && stream.Codec != "h264"))) {
			return mediaError("invalid")
		}
	}
	if kind != "image" {
		if out.Format.Name != "mov,mp4,m4a,3gp,3g2,mj2" || difference(out.durationMS, source.durationMS) > durationTolerance(source.durationMS) {
			return mediaError("invalid")
		}
	}
	if kind == "image" {
		if out.Format.Name != "png_pipe" || out.width > source.width || out.height > source.height {
			return mediaError("invalid")
		}
		if !economy && (out.width != source.width || out.height != source.height) {
			return mediaError("invalid")
		}
		if economy && (out.width > 1600 || out.height > 1600) {
			return mediaError("invalid")
		}
	}
	if kind == "video" {
		width, height := 1920, 1080
		if economy {
			width, height = 640, 360
		}
		if out.width > width || out.height > height || out.width > source.width || out.height > source.height {
			return mediaError("invalid")
		}
	}
	return nil
}
func difference(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// Permit codec packet rounding, but never hide truncation of a short clip.
func durationTolerance(durationMS int64) int64 {
	return min(int64(250), max(int64(25), durationMS/100))
}
