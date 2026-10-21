// Package themes validates and prepares private, immutable visual themes.
package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/text/unicode/norm"
	"math"
	"path"
	"siplatform/internal/media"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrUnauthorized = errors.New("themes: unauthorized")
	ErrForbidden    = errors.New("themes: forbidden")
	ErrNotFound     = errors.New("themes: not found")
	ErrConflict     = errors.New("themes: conflict")
	ErrInvalid      = errors.New("themes: invalid")
	ErrQuota        = errors.New("themes: quota")
	ErrBlocked      = errors.New("themes: blocked")
	ErrUnavailable  = errors.New("themes: unavailable")
)

type Palette struct {
	Background string `json:"background"`
	Surface    string `json:"surface"`
	SurfaceAlt string `json:"surface_alt"`
	Border     string `json:"border"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Accent     string `json:"accent"`
	AccentText string `json:"accent_text"`
	Danger     string `json:"danger"`
	Success    string `json:"success"`
	Focus      string `json:"focus"`
}

type Shape struct {
	Radius      int `json:"radius"`
	BorderWidth int `json:"border_width"`
}

type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Name          string  `json:"name"`
	Author        string  `json:"author"`
	Provenance    string  `json:"provenance"`
	Font          string  `json:"font"`
	Palette       Palette `json:"palette"`
	Cards         Shape   `json:"cards"`
	Buttons       Shape   `json:"buttons"`
	Background    string  `json:"background,omitempty"`
	Logo          string  `json:"logo,omitempty"`
	Ornament      string  `json:"ornament,omitempty"`
}

type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationError struct {
	Issues []Issue `json:"issues"`
}

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// Issues exposes only typed, public diagnostics; internal errors are never copied.
func Issues(err error) []Issue {
	var e *ValidationError
	if !errors.As(err, &e) || e == nil {
		return nil
	}
	return append([]Issue(nil), e.Issues...)
}
func invalid(path, code, message string) error {
	return &ValidationError{Issues: []Issue{{Path: path, Code: code, Message: message}}}
}

type Asset struct {
	Name     string         `json:"name"`
	Prepared media.Prepared `json:"prepared"`
}

type Prepared struct {
	Manifest     Manifest `json:"manifest"`
	Assets       []Asset  `json:"assets"`
	Hash         string   `json:"hash"`
	OriginalKey  string   `json:"original_key"`
	OriginalSize int64    `json:"original_size"`
	SizeBytes    int64    `json:"size_bytes"`
}

// Validate checks the complete trusted theme profile before persistence or use.
func (p Prepared) Validate() error {
	if err := validateManifest(p.Manifest); err != nil {
		return err
	}
	if !hashKey(p.OriginalKey) || p.OriginalSize <= 0 || p.OriginalSize > maxArchiveBytes {
		return invalid("original_key", "invalid", "Некорректный исходный архив")
	}
	references := manifestReferences(p.Manifest)
	if len(p.Assets) != len(references) || len(p.Assets) > 3 {
		return invalid("assets", "invalid", "Набор изображений не соответствует манифесту")
	}
	seen := map[string]bool{}
	sizes := map[string]int64{}
	var standard, economy int64
	for _, a := range p.Assets {
		if !validAssetPath(a.Name) || !references[a.Name] || seen[pathIdentity(a.Name)] {
			return invalid("assets", "invalid", "Неверное или повторное имя изображения")
		}
		seen[pathIdentity(a.Name)] = true
		m := a.Prepared
		if m.Kind != "image" || m.DurationMS != 0 || m.Width <= 0 || m.Height <= 0 || int64(m.Width) > 4000000/int64(m.Height) {
			return invalid(a.Name, "image_invalid", "Некорректное подготовленное изображение")
		}
		for _, v := range []media.Variant{m.Standard, m.Economy} {
			if known, ok := sizes[v.Key]; ok && known != v.Size {
				return invalid(a.Name, "image_invalid", "Размер изображения не соответствует хешу")
			}
			sizes[v.Key] = v.Size
			if !hashKey(v.Key) || v.MIME != "image/png" || v.Size <= 0 || v.Size > maxStandardBytes {
				return invalid(a.Name, "image_invalid", "Некорректное подготовленное изображение")
			}
		}
		if m.Standard.Key == m.Economy.Key && m.Standard.Size != m.Economy.Size {
			return invalid(a.Name, "image_invalid", "Размер изображения не соответствует хешу")
		}
		standard += m.Standard.Size
		economy += m.Economy.Size
	}
	if standard > maxStandardBytes || economy > maxEconomyBytes {
		return invalid("assets", "budget", "Уменьшите изображения: набор превышает допустимый объём")
	}
	if p.SizeBytes != p.OriginalSize+standard+economy {
		return invalid("size_bytes", "invalid", "Неверный объём подготовленной темы")
	}
	if !hashKey(p.Hash) || p.Hash != preparedHash(p.Manifest, p.Assets) {
		return invalid("hash", "invalid", "Неверный хеш подготовленной темы")
	}
	return nil
}

type Card struct {
	ID        string `json:"id"`
	OwnerID   string `json:"owner_id"`
	Name      string `json:"name"`
	Author    string `json:"author"`
	Revision  int    `json:"revision"`
	SizeBytes int64  `json:"size_bytes"`
	Blocked   bool   `json:"blocked"`
}

type Version struct {
	Card     Card     `json:"card"`
	Prepared Prepared `json:"prepared"`
}

const (
	maxArchiveBytes  int64  = 5 << 20
	maxUnpackedBytes uint64 = 20 << 20
	maxManifestBytes int64  = 64 << 10
	maxImageBytes    int64  = 2 << 20
	maxStandardBytes int64  = 2 << 20
	maxEconomyBytes  int64  = 1 << 20
)

func hashKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func preparedHash(m Manifest, values []Asset) string {
	assets := append([]Asset{}, values...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	b, _ := json.Marshal(struct {
		Manifest Manifest `json:"manifest"`
		Assets   []Asset  `json:"assets"`
	}{m, assets})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func manifestReferences(m Manifest) map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{m.Background, m.Logo, m.Ornament} {
		if name != "" {
			out[name] = true
		}
	}
	return out
}
func plainText(s string, limit int, multiline bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func validateManifest(m Manifest) error {
	if m.SchemaVersion != 1 {
		return invalid("schema_version", "version", "Поддерживается только версия схемы 1")
	}
	for _, v := range []struct {
		name, value string
		limit       int
		multiline   bool
	}{{"name", m.Name, 80, false}, {"author", m.Author, 120, false}, {"provenance", m.Provenance, 2000, true}} {
		if !plainText(v.value, v.limit, v.multiline) || (v.name == "name" && strings.TrimSpace(v.value) == "") {
			return invalid(v.name, "text", "Укажите простой текст допустимой длины")
		}
	}
	if m.Font != "sans" && m.Font != "serif" && m.Font != "mono" {
		return invalid("font", "font", "Выберите sans, serif или mono")
	}
	for _, v := range []struct {
		name  string
		shape Shape
	}{{"cards", m.Cards}, {"buttons", m.Buttons}} {
		if v.shape.Radius < 4 || v.shape.Radius > 24 {
			return invalid(v.name+".radius", "range", "Радиус должен быть от 4 до 24 пикселей")
		}
		if v.shape.BorderWidth < 1 || v.shape.BorderWidth > 3 {
			return invalid(v.name+".border_width", "range", "Толщина границы должна быть от 1 до 3 пикселей")
		}
	}
	colors := []struct{ name, value string }{{"background", m.Palette.Background}, {"surface", m.Palette.Surface}, {"surface_alt", m.Palette.SurfaceAlt}, {"border", m.Palette.Border}, {"text", m.Palette.Text}, {"muted", m.Palette.Muted}, {"accent", m.Palette.Accent}, {"accent_text", m.Palette.AccentText}, {"danger", m.Palette.Danger}, {"success", m.Palette.Success}, {"focus", m.Palette.Focus}}
	for _, c := range colors {
		if len(c.value) != 7 || c.value[0] != '#' {
			return invalid("palette."+c.name, "color", "Нужен непрозрачный цвет #RRGGBB")
		}
		if _, err := hex.DecodeString(c.value[1:]); err != nil {
			return invalid("palette."+c.name, "color", "Нужен непрозрачный цвет #RRGGBB")
		}
	}
	for _, surface := range []string{m.Palette.Background, m.Palette.Surface, m.Palette.SurfaceAlt} {
		for _, c := range []struct {
			name, value string
			minimum     float64
		}{{"text", m.Palette.Text, 4.5}, {"muted", m.Palette.Muted, 4.5}, {"danger", m.Palette.Danger, 4.5}, {"success", m.Palette.Success, 4.5}, {"focus", m.Palette.Focus, 3}, {"border", m.Palette.Border, 3}} {
			if contrast(c.value, surface) < c.minimum {
				return invalid("palette."+c.name, "contrast", "Увеличьте контраст цвета на всех поверхностях")
			}
		}
	}
	if contrast(m.Palette.AccentText, m.Palette.Accent) < 4.5 {
		return invalid("palette.accent_text", "contrast", "Увеличьте контраст текста кнопки")
	}
	for _, r := range []struct{ name, value string }{{"background", m.Background}, {"logo", m.Logo}, {"ornament", m.Ornament}} {
		if r.value != "" && !validAssetPath(r.value) {
			return invalid(r.name, "path", "Укажите путь к PNG, JPEG или WebP в assets/")
		}
	}
	return nil
}
func luminance(s string) float64 {
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	sum := 0.0
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		c := float64((v>>uint(16-8*i))&255) / 255
		if c <= 0.04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		sum += c * w
	}
	return sum
}
func contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
func pathIdentity(s string) string { return strings.ToLower(norm.NFC.String(s)) }
func safePath(s string) bool {
	if s == "" || len(s) > 512 || !plainText(s, 512, false) || strings.ContainsAny(s, "\\:%?#") || strings.HasPrefix(s, "/") || norm.NFC.String(s) != s {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(s, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
			return false
		}
	}
	return true
}
func validAssetPath(s string) bool {
	if !safePath(s) || !strings.HasPrefix(s, "assets/") || strings.HasSuffix(s, "/") {
		return false
	}
	switch strings.ToLower(path.Ext(s)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return true
	}
	return false
}
