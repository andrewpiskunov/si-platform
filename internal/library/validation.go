package library

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"siplatform/internal/media"
	"siplatform/internal/packs"
	"siplatform/internal/playpack"
	"siplatform/internal/siq"
)

func hexID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Match SIQ's canonical names: aliases after percent decoding, NFC and case
// folding must not point at different assets in the same immutable revision.
func assetName(name string) (string, error) {
	if len(name) > 1024 || !utf8.ValidString(name) {
		return "", ErrInvalid
	}
	for i := 0; i < 4 && strings.Contains(name, "%"); i++ {
		decoded, err := url.PathUnescape(name)
		if err != nil {
			return "", ErrInvalid
		}
		if decoded == name {
			break
		}
		name = decoded
	}
	if name == "" || strings.ContainsAny(name, "\\:%") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return "", ErrInvalid
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "", ErrInvalid
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalid
		}
	}
	name = strings.ToLower(norm.NFC.String(name))
	switch path.Ext(name) {
	case ".zip", ".siq", ".7z", ".rar", ".tar", ".gz":
		return "", ErrInvalid
	}
	return name, nil
}
func prepare(ctx context.Context, in Save) (Draft, error) {
	if err := ctx.Err(); err != nil {
		return Draft{}, err
	}
	data, err := json.Marshal(in.Document)
	if err != nil {
		return Draft{}, invalid("document encoding")
	}
	if len(data) > 10<<20 {
		return Draft{}, ErrQuota
	}
	if len(in.Assets) > 5000 {
		return Draft{}, ErrQuota
	}
	if in.OriginalKey != "" && !hexID(in.OriginalKey, 64) {
		return Draft{}, invalid("original key")
	}
	d := Draft{AssetIssues: append([]packs.Issue{}, in.Issues...), Document: in.Document, Assets: append([]Asset{}, in.Assets...), OriginalKey: in.OriginalKey, Issues: []packs.Issue{}}
	d.Card = Card{Name: in.Document.Name, Author: strings.Join(in.Document.Info.Authors, ", "), Status: "draft", Language: in.Document.Language, Restriction: in.Document.Restriction, Difficulty: in.Document.Difficulty, RoundCount: len(in.Document.Rounds), Tags: append([]string{}, in.Document.Tags...)}
	seen := map[string]bool{"content.xml": true}
	type objectMetadata struct {
		Size int64
		MIME string
	}
	keys := map[string]objectMetadata{}
	sourceAssets := []siq.Asset{}
	var total int64
	var derivatives int64
	for _, asset := range in.Assets {
		name, err := assetName(asset.Name)
		if err != nil || seen[name] || !hexID(asset.Key, 64) || asset.Size < 0 || len(asset.ContentType) > 255 || strings.ContainsAny(asset.ContentType, "\r\n\x00") {
			return Draft{}, invalid("asset")
		}
		if prior, exists := keys[asset.Key]; exists && (prior.Size != asset.Size || prior.MIME != asset.ContentType) {
			return Draft{}, invalid("inconsistent content key")
		}
		seen[name] = true
		keys[asset.Key] = objectMetadata{asset.Size, asset.ContentType}
		preparedBytes, err := preparedSize(asset)
		if err != nil {
			return Draft{}, invalid("prepared media")
		}
		if asset.Prepared != nil {
			for _, variant := range []media.Variant{asset.Prepared.Standard, asset.Prepared.Economy} {
				if prior, exists := keys[variant.Key]; exists && (prior.Size != variant.Size || prior.MIME != variant.MIME) {
					return Draft{}, invalid("inconsistent prepared content key")
				}
				keys[variant.Key] = objectMetadata{variant.Size, variant.MIME}
			}
		}
		if derivatives > 1<<30-preparedBytes {
			return Draft{}, ErrQuota
		}
		derivatives += preparedBytes
		if asset.Size > 1<<30 || total > 1<<30-asset.Size {
			return Draft{}, ErrQuota
		}
		total += asset.Size
		sourceAssets = append(sourceAssets, siq.Asset{Name: asset.Name, Key: name, Size: asset.Size, SHA256: asset.Key})
	}
	if total > 1<<30-derivatives {
		return Draft{}, ErrQuota
	}
	d.Card.SizeBytes = int64(len(data)) + total + derivatives
	d.Card.HasMedia = len(in.Assets) > 0 || in.Document.Logo != ""
	for _, round := range in.Document.Rounds {
		for _, theme := range round.Themes {
			d.Card.QuestionCount += len(theme.Questions)
		}
	}
	issues := packs.Validate(in.Document)
	if packs.Playable(issues) {
		issues, err = siq.ValidateDocument(ctx, in.Document, sourceAssets)
		if err != nil {
			if ctx.Err() != nil {
				return Draft{}, ctx.Err()
			}
			issues = append(issues, packs.Issue{Severity: "error", Code: "invalid_export", Path: "document", Message: "Документ не может быть экспортирован в SIQ"})
		}
	}
	limits := siq.DefaultLimits()
	if len(in.Assets)+1 > limits.Files || total >= limits.UnpackedBytes {
		issues = append(issues, packs.Issue{Severity: "error", Code: "export_limit", Path: "assets", Message: "Файлы превышают допустимый размер или количество для экспорта SIQ"})
	}
	for _, asset := range in.Assets {
		if asset.Size > limits.MediaBytes {
			issues = append(issues, packs.Issue{Severity: "error", Code: "export_limit", Path: "assets", Message: "Медиафайл превышает допустимый размер для экспорта SIQ"})
			break
		}
	}
	seenIssues := map[packs.Issue]bool{}
	for _, issue := range append(issues, in.Issues...) {
		if !seenIssues[issue] {
			d.Issues = append(d.Issues, issue)
			seenIssues[issue] = true
		}
	}
	refreshMediaDiagnostics(&d)
	d.PlayIssues = playpack.ValidateMedia(d.Document, MediaLookup(d.Assets))
	return d, nil
}
