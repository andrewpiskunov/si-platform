// Package packs defines the private, editable quiz model shared by SIQ, authoring and game services.
package packs

type Document struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	SourceVersion string   `json:"source_version,omitempty"`
	Date          string   `json:"date,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	Language      string   `json:"language,omitempty"`
	Restriction   string   `json:"restriction,omitempty"`
	Difficulty    int      `json:"difficulty,omitempty"`
	Logo          string   `json:"logo,omitempty"`
	ContactURI    string   `json:"contact_uri,omitempty"`
	Generator     string   `json:"generator,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Info          Info     `json:"info"`
	GlobalXML     string   `json:"global_xml,omitempty"`
	Rounds        []Round  `json:"rounds"`
	ImportIssues  []Issue  `json:"import_issues,omitempty"`
}
type Info struct {
	Authors         []string `json:"authors,omitempty"`
	Sources         []string `json:"sources,omitempty"`
	Comments        string   `json:"comments,omitempty"`
	ShowmanComments string   `json:"showman_comments,omitempty"`
	Extension       string   `json:"extension,omitempty"`
}
type Round struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Type   string  `json:"type,omitempty"`
	Info   Info    `json:"info"`
	Themes []Theme `json:"themes"`
}
type Theme struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Info      Info       `json:"info"`
	Questions []Question `json:"questions"`
}
type Question struct {
	ID        string      `json:"id"`
	Price     int         `json:"price"`
	Type      string      `json:"type,omitempty"`
	Info      Info        `json:"info"`
	Params    []Parameter `json:"params"`
	Right     []string    `json:"right"`
	Wrong     []string    `json:"wrong,omitempty"`
	HasScript bool        `json:"has_script,omitempty"`
}
type Parameter struct {
	Name      string      `json:"name"`
	Type      string      `json:"type,omitempty"`
	Value     string      `json:"value,omitempty"`
	Items     []Content   `json:"items,omitempty"`
	Params    []Parameter `json:"params,omitempty"`
	NumberSet *NumberSet  `json:"number_set,omitempty"`
}
type NumberSet struct {
	Value   string `json:"value,omitempty"`
	Minimum *int   `json:"minimum,omitempty"`
	Maximum *int   `json:"maximum,omitempty"`
	Step    *int   `json:"step,omitempty"`
}
type Content struct {
	Type          string `json:"type"`
	Value         string `json:"value"`
	IsRef         bool   `json:"is_ref,omitempty"`
	Placement     string `json:"placement,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	WaitForFinish bool   `json:"wait_for_finish,omitempty"`
}
type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

func (q Question) Content(name string) []Content {
	for _, p := range q.Params {
		if p.Name == name {
			return p.Items
		}
	}
	return nil
}
func Playable(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return false
		}
	}
	return true
}
