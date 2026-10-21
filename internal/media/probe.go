package media

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"strconv"
	"strings"
)

const demuxers = "png_pipe,jpeg_pipe,webp_pipe,wav,mp3,flac,aac,ogg,mov,matroska,webm,avi,mpeg,mpegts"

type probeStream struct {
	Type       string `json:"codec_type"`
	Codec      string `json:"codec_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Duration   string `json:"duration"`
	Frames     string `json:"nb_frames"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
}
type metadata struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Name     string `json:"format_name"`
		Duration string `json:"duration"`
	} `json:"format"`
	durationMS    int64
	width, height int
}

func parseProbe(b []byte, kind string) (metadata, error) {
	var m metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return m, mediaError("invalid")
	}
	allowed := false
	for _, f := range strings.Split(m.Format.Name, ",") {
		if strings.Contains(","+demuxers+",", ","+f+",") && f != "" {
			allowed = true
		}
	}
	if !allowed {
		return m, mediaError("unsupported")
	}
	if len(m.Streams) == 0 || len(m.Streams) > 2 {
		return m, mediaError("unsupported")
	}
	images := map[string]bool{"png": true, "mjpeg": true, "webp": true}
	video, audio := 0, 0
	for _, s := range m.Streams {
		switch s.Type {
		case "video":
			video++
			m.width = s.Width
			m.height = s.Height
			if s.Width <= 0 || s.Height <= 0 {
				return m, mediaError("invalid")
			}
			if kind == "image" {
				if !images[s.Codec] {
					return m, mediaError("unsupported")
				}
				if int64(s.Width) > 16000000/int64(s.Height) {
					return m, mediaError("too_large")
				}
				if s.Frames != "" && s.Frames != "N/A" && s.Frames != "1" {
					return m, mediaError("unsupported")
				}
			} else if s.Width > 3840 || s.Height > 2160 {
				return m, mediaError("too_large")
			}
		case "audio":
			audio++
			if s.Channels < 0 || s.Channels > 8 {
				return m, mediaError("unsupported")
			}
			if s.SampleRate != "" {
				rate, err := strconv.Atoi(s.SampleRate)
				if err != nil || rate <= 0 || rate > 192000 {
					return m, mediaError("unsupported")
				}
			}
		default:
			return m, mediaError("unsupported")
		}
	}
	if (kind == "image" && (video != 1 || audio != 0)) || (kind == "audio" && (video != 0 || audio != 1)) || (kind == "video" && (video != 1 || audio > 1)) {
		return m, mediaError("unsupported")
	}
	if kind == "image" {
		if m.Format.Name != "png_pipe" && m.Format.Name != "jpeg_pipe" && m.Format.Name != "webp_pipe" {
			return m, mediaError("unsupported")
		}
		return m, nil
	}
	seconds := 0.0
	durations := []string{m.Format.Duration}
	for _, s := range m.Streams {
		durations = append(durations, s.Duration)
	}
	for _, d := range durations {
		if d == "" || d == "N/A" {
			continue
		}
		v, err := strconv.ParseFloat(d, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return m, mediaError("invalid")
		}
		if v > 600 {
			return m, mediaError("too_long")
		}
		seconds = math.Max(seconds, v)
	}
	if seconds <= 0 {
		return m, mediaError("invalid")
	}
	m.durationMS = int64(math.Ceil(seconds * 1000))
	return m, nil
}

// Image headers are checked without allocating the decoded image. Chunk scanning
// rejects animation that a single-frame decoder might otherwise silently ignore.
func checkImage(r io.ReadSeeker) error {
	var head [12]byte
	n, _ := io.ReadFull(r, head[:])
	_, _ = r.Seek(0, io.SeekStart)
	if n >= 8 && bytes.Equal(head[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		cfg, _, err := image.DecodeConfig(r)
		if err != nil {
			return mediaError("invalid")
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > 16000000/int64(cfg.Height) {
			return mediaError("too_large")
		}
		_, _ = r.Seek(8, io.SeekStart)
		for {
			var h [8]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return mediaError("invalid")
			}
			size := int64(binary.BigEndian.Uint32(h[:4]))
			kind := string(h[4:])
			if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
				return mediaError("unsupported")
			}
			if kind == "IEND" {
				return nil
			}
			if size > maxBytes {
				return mediaError("invalid")
			}
			if _, err := r.Seek(size+4, io.SeekCurrent); err != nil {
				return mediaError("invalid")
			}
		}
	}
	if n >= 2 && head[0] == 0xff && head[1] == 0xd8 {
		cfg, _, err := image.DecodeConfig(r)
		if err != nil {
			return mediaError("invalid")
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > 16000000/int64(cfg.Height) {
			return mediaError("too_large")
		}
		return nil
	}
	if n == 12 && string(head[:4]) == "RIFF" && string(head[8:]) == "WEBP" {
		total := int64(binary.LittleEndian.Uint32(head[4:8])) + 8
		if total > maxBytes || total < 20 {
			return mediaError("invalid")
		}
		_, _ = r.Seek(12, io.SeekStart)
		for pos := int64(12); pos < total; {
			var h [8]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return mediaError("invalid")
			}
			size := int64(binary.LittleEndian.Uint32(h[4:]))
			kind := string(h[:4])
			if kind == "ANIM" || kind == "ANMF" {
				return mediaError("unsupported")
			}
			if size > total-pos-8 {
				return mediaError("invalid")
			}
			if _, err := r.Seek(size+(size&1), io.SeekCurrent); err != nil {
				return mediaError("invalid")
			}
			pos += 8 + size + (size & 1)
		}
		return nil
	}
	return mediaError("unsupported")
}
