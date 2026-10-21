package themes

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"time"

	"siplatform/internal/media"
)

// Result owns only the media processor results produced by this preparation.
type Result struct {
	Prepared Prepared
	Files    []media.Output
	results  []*media.Result
}

func (r *Result) Close() error {
	if r == nil {
		return nil
	}
	var first error
	for _, owned := range r.results {
		if err := owned.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Prepare converts every distinct referenced image through the shared sandbox.
// The caller owns the result until all verified outputs have been persisted.
func Prepare(ctx context.Context, processor *media.Processor, a *Archive) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if a == nil {
		return nil, invalid("archive", "invalid", "Архив отсутствует")
	}
	if len(a.images) > 0 && processor == nil {
		return nil, invalid("assets", "media_unavailable", "Подготовка изображений недоступна")
	}
	result := &Result{Prepared: Prepared{Manifest: a.manifest, Assets: []Asset{}, OriginalKey: a.originalKey, OriginalSize: a.originalSize, SizeBytes: a.originalSize}}
	successful := false
	defer func() {
		if !successful {
			_ = result.Close()
		}
	}()
	var standard, economy int64
	for _, name := range a.imageNames() {
		data := a.images[name]
		if err := checkImageSize(data); err != nil {
			return nil, invalid(name, "image_invalid", "Нужно статическое изображение PNG, JPEG или WebP до 2 МиБ и 4 мегапикселей")
		}
		prepared, err := processor.Prepare(ctx, "image", bytes.NewReader(data), int64(len(data)))
		if ctx.Err() != nil {
			if prepared != nil {
				_ = prepared.Close()
			}
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, invalid(name, "media_"+media.Code(err), mediaMessage(media.Code(err)))
		}
		if prepared == nil {
			return nil, invalid(name, "image_invalid", "Не удалось подготовить изображение")
		}
		result.results = append(result.results, prepared)
		result.Prepared.Assets = append(result.Prepared.Assets, Asset{Name: name, Prepared: prepared.Prepared})
		result.Files = append(result.Files, prepared.Files...)
		standard += prepared.Prepared.Standard.Size
		economy += prepared.Prepared.Economy.Size
		if standard > maxStandardBytes || economy > maxEconomyBytes {
			return nil, invalid("assets", "budget", "Уменьшите изображения: обычный набор должен быть до 2 МиБ, экономный — до 1 МиБ")
		}
	}
	result.Prepared.SizeBytes += standard + economy
	result.Prepared.Hash = preparedHash(a.manifest, result.Prepared.Assets)
	if err := result.Prepared.Validate(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	successful = true
	return result, nil
}

func mediaMessage(code string) string {
	switch code {
	case "unavailable":
		return "Подготовка изображений временно недоступна"
	case "too_large":
		return "Уменьшите размер изображения"
	case "timeout":
		return "Превышено время подготовки изображения"
	default:
		return "Изображение повреждено, анимировано или имеет неподдерживаемый формат"
	}
}

// Header inspection bounds pixel allocation before the full sandbox decode.
// The shared processor remains responsible for animation and complete decoding.
func checkImageSize(data []byte) error {
	reject := func() error { return ErrInvalid }
	if len(data) == 0 || int64(len(data)) > maxImageBytes {
		return reject()
	}
	width, height := 0, 0
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		if uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
			return reject()
		}
		for off := 12; off+8 <= len(data); {
			kind := string(data[off : off+4])
			size := uint64(binary.LittleEndian.Uint32(data[off+4 : off+8]))
			off += 8
			if size > uint64(len(data)-off) {
				return reject()
			}
			chunk := data[off : off+int(size)]
			switch kind {
			case "ANIM", "ANMF":
				return reject()
			case "VP8X":
				if len(chunk) != 10 || chunk[0]&2 != 0 {
					return reject()
				}
				width = 1 + int(chunk[4]) + int(chunk[5])<<8 + int(chunk[6])<<16
				height = 1 + int(chunk[7]) + int(chunk[8])<<8 + int(chunk[9])<<16
			case "VP8 ":
				if len(chunk) < 10 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
					return reject()
				}
				width = int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
				height = int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			case "VP8L":
				if len(chunk) < 5 || chunk[0] != 0x2f {
					return reject()
				}
				bits := binary.LittleEndian.Uint32(chunk[1:5])
				width = int(bits&0x3fff) + 1
				height = int((bits>>14)&0x3fff) + 1
			}
			if width > 0 && height > 0 && int64(width) > 4000000/int64(height) {
				return reject()
			}
			off += int(size + size%2)
		}
	} else {
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" && format != "jpeg" {
			return reject()
		}
		width, height = config.Width, config.Height
	}
	if width <= 0 || height <= 0 || int64(width) > 4000000/int64(height) {
		return reject()
	}
	return nil
}
