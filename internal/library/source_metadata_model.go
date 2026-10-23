package library

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type SourceLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type SourcePercent struct {
	Name    string `json:"name"`
	Percent int    `json:"percent"`
}
type SourceTheme struct {
	Name      string   `json:"name"`
	Questions []string `json:"questions"`
}
type SourceRound struct {
	Name   string        `json:"name"`
	Themes []SourceTheme `json:"themes"`
}
type SourceMetadata struct {
	Name          string          `json:"name"`
	Authors       []SourceLink    `json:"authors"`
	Description   []string        `json:"description"`
	Links         []SourceLink    `json:"links"`
	Tags          []SourceLink    `json:"tags"`
	PublishedDate string          `json:"published_date"`
	Filename      string          `json:"filename"`
	FileSizeLabel string          `json:"file_size_label"`
	QuestionCount *int            `json:"question_count"`
	Downloads     *int64          `json:"downloads"`
	Media         []SourcePercent `json:"media"`
	Categories    []SourcePercent `json:"categories"`
	Originals     []SourceLink    `json:"originals"`
	CoverURL      string          `json:"cover_url"`
	Rounds        []SourceRound   `json:"rounds"`
}
type SourceSummary struct {
	ExternalID    string       `json:"external_id"`
	Authors       []SourceLink `json:"authors"`
	PublishedDate string       `json:"published_date"`
	Downloads     *int64       `json:"downloads"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

var sourceIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func SafeSourceLink(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && len(raw) <= 2048 && !strings.ContainsAny(raw, "\r\n\x00")
}
func ValidSourceCover(id, raw string) bool {
	return raw == "" || sourceIDPattern.MatchString(id) && raw == "https://www.sibrowser.ru/packages/"+id+"/logo.webp"
}
func (m SourceMetadata) Validate(id string) error {
	b, e := json.Marshal(m)
	if e != nil || len(b) > 512<<10 || !sourceIDPattern.MatchString(id) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 4000 || m.Filename == "" || len(m.Authors) == 0 || len(m.Authors) > 100 || !ValidSourceCover(id, m.CoverURL) {
		return ErrInvalid
	}
	if m.PublishedDate != "" {
		if _, e = time.Parse("2006-01-02", m.PublishedDate); e != nil {
			return ErrInvalid
		}
	}
	if m.QuestionCount != nil && (*m.QuestionCount < 0 || *m.QuestionCount > 100000) || m.Downloads != nil && *m.Downloads < 0 {
		return ErrInvalid
	}
	for _, ls := range [][]SourceLink{m.Authors, m.Tags, m.Links, m.Originals} {
		for _, l := range ls {
			if l.Name == "" || len(l.Name) > 4000 || !SafeSourceLink(l.URL) {
				return ErrInvalid
			}
		}
	}
	for _, ps := range [][]SourcePercent{m.Media, m.Categories} {
		for _, p := range ps {
			if p.Name == "" || p.Percent < 0 || p.Percent > 100 {
				return ErrInvalid
			}
		}
	}
	return nil
}
