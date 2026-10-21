// Package library stores private immutable quiz revisions and moderated publications.
package library

import (
	"errors"
	"siplatform/internal/media"
	"siplatform/internal/packs"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid")
	ErrBlocked      = errors.New("blocked")
	ErrQuota        = errors.New("quota")
)

type Asset struct {
	Name        string          `json:"name"`
	Key         string          `json:"key"`
	ContentType string          `json:"content_type"`
	Size        int64           `json:"size"`
	Prepared    *media.Prepared `json:"prepared,omitempty"`
}
type Rights struct {
	License       string `json:"license"`
	SourceURL     string `json:"source_url"`
	Attribution   string `json:"attribution"`
	AllowDownload bool   `json:"allow_download"`
	Confirmed     bool   `json:"confirmed"`
}
type Card struct {
	PublishedRevision int       `json:"published_revision"`
	PendingRevision   int       `json:"pending_revision"`
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	OwnerID           string    `json:"owner_id"`
	Author            string    `json:"author"`
	Status            string    `json:"status"`
	Language          string    `json:"language"`
	Restriction       string    `json:"restriction"`
	Revision          int       `json:"revision"`
	Difficulty        int       `json:"difficulty"`
	QuestionCount     int       `json:"question_count"`
	RoundCount        int       `json:"round_count"`
	SizeBytes         int64     `json:"size_bytes"`
	HasMedia          bool      `json:"has_media"`
	AllowDownload     bool      `json:"allow_download"`
	Tags              []string  `json:"tags"`
	Rights            Rights    `json:"rights"`
	UpdatedAt         time.Time `json:"updated_at"`
}
type Draft struct {
	PlayIssues  []packs.Issue  `json:"play_issues"`
	AssetIssues []packs.Issue  `json:"asset_issues,omitempty"`
	Card        Card           `json:"card"`
	Document    packs.Document `json:"document"`
	Assets      []Asset        `json:"assets"`
	OriginalKey string         `json:"original_key"`
	Issues      []packs.Issue  `json:"issues"`
}

// Save is a trusted application input; Issues must never be populated directly from HTTP JSON.
type Save struct {
	// ImportID makes a trusted importer retry idempotent for this owner.
	ImportID    string
	Document    packs.Document
	Assets      []Asset
	OriginalKey string
	Issues      []packs.Issue
}
type Filter struct {
	Query, Language, Tag, Age string
	Difficulty                *int
	HasMedia                  *bool
	MaxBytes                  int64
	Offset, Limit             int
}
type DecisionRecord struct {
	ID        int64     `json:"id"`
	Revision  int       `json:"revision"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}
type DecisionPage struct {
	Items       []DecisionRecord `json:"items"`
	NextAfterID int64            `json:"next_after_id,omitempty"`
}
type ReportsPage struct {
	Items     []Report `json:"items"`
	NextAfter string   `json:"next_after,omitempty"`
}
type Report struct {
	Resolution       string     `json:"resolution"`
	ResolutionReason string     `json:"resolution_reason"`
	ResolvedBy       *string    `json:"resolved_by"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	ID               string     `json:"id"`
	PackID           string     `json:"pack_id"`
	ReporterID       string     `json:"reporter_id"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	Revision         int        `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
}
