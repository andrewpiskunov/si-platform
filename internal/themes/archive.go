package themes

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// Archive holds validated bounded entries; it does not own its source reader.
// Keep that reader open until preparation completes.
type Archive struct {
	manifest     Manifest
	images       map[string][]byte
	originalKey  string
	originalSize int64
}

// Read validates every ZIP entry, including CRC and actual decompressed sizes.
// No archive entry is extracted to a filesystem or read without a byte limit.
func Read(ctx context.Context, source io.ReaderAt, size int64) (*Archive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || size <= 0 || size > maxArchiveBytes {
		return nil, invalid("archive", "size", "Архив должен занимать не более 5 МиБ")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, contextReader{ctx, io.NewSectionReader(source, 0, size)})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || n != size {
		return nil, invalid("archive", "invalid", "Не удалось прочитать исходный архив полностью")
	}
	z, err := zip.NewReader(contextReaderAt{ctx, io.NewSectionReader(source, 0, size)}, size)
	if err != nil || len(z.File) == 0 || len(z.File) > 50 {
		return nil, invalid("archive", "entries", "Архив повреждён или содержит более 50 записей")
	}
	a := &Archive{images: map[string][]byte{}, originalKey: hex.EncodeToString(hash.Sum(nil)), originalSize: size}
	seen := map[string]bool{}
	var total uint64
	var manifest []byte
	for _, f := range z.File {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := f.Name
		identity := pathIdentity(strings.TrimSuffix(name, "/"))
		if !safePath(name) || seen[identity] {
			return nil, invalid("archive", "path", "Архив содержит небезопасный или повторный путь")
		}
		seen[identity] = true
		offset, offsetErr := f.DataOffset()
		if offsetErr != nil || offset < 0 || offset > size || f.CompressedSize64 > uint64(size-offset) {
			return nil, invalid(name, "size", "Сжатый файл выходит за границы архива")
		}
		mode := f.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() || f.Flags&1 != 0 {
			return nil, invalid(name, "type", "Ссылки, специальные и зашифрованные файлы запрещены")
		}
		if f.UncompressedSize64 > maxUnpackedBytes-total {
			return nil, invalid(name, "size", "Распакованный архив превышает 20 МиБ")
		}
		total += f.UncompressedSize64
		limit := maxImageBytes
		if mode.IsDir() {
			limit = 0
			if !strings.HasSuffix(name, "/") {
				return nil, invalid(name, "path", "Некорректный путь каталога")
			}
		} else if name == "theme.json" {
			limit = maxManifestBytes
		} else if !validAssetPath(name) {
			return nil, invalid(name, "file", "Допустимы только theme.json и изображения в assets/")
		}
		if f.UncompressedSize64 > uint64(limit) {
			return nil, invalid(name, "size", "Файл превышает допустимый размер")
		}
		r, err := f.Open()
		if err != nil {
			return nil, invalid(name, "invalid", "Не удалось открыть файл архива")
		}
		b, readErr := io.ReadAll(io.LimitReader(contextReader{ctx, r}, limit+1))
		closeErr := r.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if readErr != nil || closeErr != nil || int64(len(b)) > limit || uint64(len(b)) != f.UncompressedSize64 {
			return nil, invalid(name, "invalid", "Файл повреждён: неверный размер или контрольная сумма")
		}
		if mode.IsDir() {
			continue
		}
		if name == "theme.json" {
			manifest = b
		} else {
			a.images[name] = b
		}
	}
	if manifest == nil {
		return nil, invalid("theme.json", "missing", "В архиве отсутствует theme.json")
	}
	if err := parseManifest(manifest, &a.manifest); err != nil {
		return nil, err
	}
	refs := manifestReferences(a.manifest)
	if len(a.images) > 3 || len(a.images) != len(refs) {
		return nil, invalid("assets", "references", "В архиве должны быть только используемые изображения, не более трёх")
	}
	for name := range refs {
		if _, ok := a.images[name]; !ok {
			return nil, invalid(name, "missing", "Изображение отсутствует в архиве")
		}
	}
	return a, nil
}

func parseManifest(b []byte, m *Manifest) error {
	if !utf8.Valid(b) {
		return invalid("theme.json", "json", "Манифест должен содержать корректный UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	allowed := map[string]map[string]bool{
		"":        fields("schema_version name author provenance font palette cards buttons background logo ornament"),
		"palette": fields("background surface surface_alt border text muted accent accent_text danger success focus"),
		"cards":   fields("radius border_width"), "buttons": fields("radius border_width"),
	}
	if err := jsonObject(d, "", allowed); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid("theme.json", "json", "После манифеста есть лишние данные")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(m); err != nil {
		return invalid("theme.json", "json", "Неверный тип поля манифеста")
	}
	return validateManifest(*m)
}
func fields(names string) map[string]bool {
	out := map[string]bool{}
	for _, s := range strings.Fields(names) {
		out[s] = true
	}
	return out
}
func jsonObject(d *json.Decoder, path string, allowed map[string]map[string]bool) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid("theme.json", "json", "Ожидается объект JSON")
	}
	seen := map[string]bool{}
	for d.More() {
		keyToken, err := d.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return invalid("theme.json", "json", "Некорректный JSON")
		}
		field := key
		if path != "" {
			field = path + "." + key
		}
		if !allowed[path][key] {
			return invalid("theme.json", "unknown_field", "Манифест содержит неизвестное поле")
		}
		if seen[key] {
			return invalid(field, "duplicate_field", "Поле повторяется")
		}
		seen[key] = true
		if _, nested := allowed[field]; nested {
			if err := jsonObject(d, field, allowed); err != nil {
				return err
			}
			continue
		}
		value, err := d.Token()
		if err != nil || value == nil {
			return invalid(field, "json", "Поле должно иметь указанное схемой значение")
		}
		if _, nested := value.(json.Delim); nested {
			return invalid(field, "json", "Вложенные значения здесь запрещены")
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return invalid("theme.json", "json", "Некорректный JSON")
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

type contextReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (r contextReaderAt) ReadAt(b []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.ReadAt(b, off)
}
func (a *Archive) imageNames() []string {
	names := make([]string, 0, len(a.images))
	for name := range a.images {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
