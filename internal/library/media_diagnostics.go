package library

import (
	"strings"

	"siplatform/internal/packs"
)

// Preparation failures describe the imported file at the time of import, not
// every subsequent revision. Keep AssetIssues intact for inspection, while
// deriving their effective severity from this revision's references and assets.
// Both writes and reads use this derivation, including revisions saved before it
// existed. Publication and export retain their document-validation gates; current
// unsupported or unprepared game content also remains subject to PlayIssues.
func refreshMediaDiagnostics(d *Draft) {
	if len(d.AssetIssues) == 0 {
		return
	}
	referenced := map[string]bool{}
	reference := func(kind, value string) {
		folder := map[string]string{"image": "Images/", "audio": "Audio/", "video": "Video/"}[kind]
		if folder != "" {
			if name, err := assetName(folder + value); err == nil {
				referenced[name] = true
			}
		}
	}
	if strings.HasPrefix(d.Document.Logo, "@") {
		reference("image", strings.TrimPrefix(d.Document.Logo, "@"))
	}
	var visit func([]packs.Parameter)
	visit = func(params []packs.Parameter) {
		for _, param := range params {
			for _, item := range param.Items {
				if item.IsRef {
					reference(item.Type, item.Value)
				}
			}
			visit(param.Params)
		}
	}
	for _, round := range d.Document.Rounds {
		for _, theme := range round.Themes {
			for _, question := range theme.Questions {
				visit(question.Params)
			}
		}
	}
	assets := map[string]Asset{}
	for _, asset := range d.Assets {
		if name, err := assetName(asset.Name); err == nil {
			assets[name] = asset
		}
	}
	// Source/document diagnostics are never reclassified, even if an importer
	// supplied an identical issue in AssetIssues as well.
	sourceIssues := map[packs.Issue]bool{}
	for _, issue := range d.Document.ImportIssues {
		issue.Severity = ""
		sourceIssues[issue] = true
	}
	severity := map[packs.Issue]string{}
	for _, issue := range d.AssetIssues {
		if issue.Severity != "error" {
			continue
		}
		switch issue.Code {
		case "media_pending", "media_unavailable", "media_invalid", "media_unsupported", "media_too_large", "media_too_long", "media_timeout", "media_canceled", "unsafe_media":
		default:
			continue
		}
		name, err := assetName(issue.Path)
		asset, exists := assets[name]
		if err != nil || !exists {
			continue
		}
		issue.Severity = ""
		if sourceIssues[issue] {
			continue
		}
		severity[issue] = "error"
		_, preparedErr := preparedSize(asset)
		if !referenced[name] || asset.Prepared != nil && preparedErr == nil {
			severity[issue] = "warning"
		}
	}
	for i, issue := range d.Issues {
		issue.Severity = ""
		if effective, ok := severity[issue]; ok {
			d.Issues[i].Severity = effective
		}
	}
}
