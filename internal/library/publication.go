package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/packs"
)

func label(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, '\x00')
}
func validRights(r Rights) bool {
	if !r.Confirmed || !label(r.License, 500) || !label(r.Attribution, 2000) || len(r.SourceURL) > 2048 {
		return false
	}
	if r.SourceURL == "original" {
		return true
	}
	u, err := url.ParseRequestURI(r.SourceURL)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}
func (s *Service) Submit(ctx context.Context, actor accounts.Identity, packID string, revision int, rights Rights) (Card, error) {
	a, err := s.begin(ctx, actor, "", true, packID)
	if err != nil {
		return Card{}, err
	}
	defer a.close()
	if err = a.owner(); err != nil {
		return Card{}, err
	}
	if a.pack.blocked {
		return Card{}, ErrBlocked
	}
	if revision <= 0 || !validRights(rights) {
		return Card{}, ErrInvalid
	}
	if a.pack.published != nil && *a.pack.published == revision || a.pack.pending != nil && *a.pack.pending == revision {
		return Card{}, ErrConflict
	}
	d, err := load(ctx, a.tx, packID, revision)
	if err != nil {
		return Card{}, err
	}
	if !packs.Playable(d.Issues) {
		return Card{}, ErrInvalid
	}
	if a.pack.pending != nil {
		if _, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='withdrawn',updated_at=clock_timestamp() WHERE pack_id=$1 AND revision=$2", packID, *a.pack.pending); err != nil {
			return Card{}, err
		}
	}
	data, err := json.Marshal(rights)
	if err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='pending',rights=$3,updated_at=clock_timestamp() WHERE pack_id=$1 AND revision=$2", packID, revision, data); err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET pending_revision=$2 WHERE id=$1", packID, revision); err != nil {
		return Card{}, err
	}
	d, err = load(ctx, a.tx, packID, revision)
	if err != nil {
		return Card{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Card{}, err
	}
	return d.Card, nil
}
func (s *Service) Withdraw(ctx context.Context, actor accounts.Identity, packID string) error {
	a, err := s.begin(ctx, actor, "", true, packID)
	if err != nil {
		return err
	}
	defer a.close()
	if err = a.owner(); err != nil {
		return err
	}
	if a.pack.blocked {
		return ErrBlocked
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='withdrawn',updated_at=clock_timestamp() WHERE pack_id=$1 AND status IN ('published','pending')", packID); err != nil {
		return err
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET published_revision=NULL,pending_revision=NULL WHERE id=$1", packID); err != nil {
		return err
	}
	return a.tx.Commit(ctx)
}

const publicJoin = " FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id AND r.revision=p.published_revision JOIN account_users u ON u.id=p.owner_id WHERE p.deleted_at IS NULL AND NOT p.blocked AND u.deleted_at IS NULL"

func (s *Service) Catalogue(ctx context.Context, f Filter) ([]Card, error) {
	if f.Offset < 0 || f.Offset > 100000 || f.Limit < 0 || f.MaxBytes < 0 || len(f.Query) > 256 || len(f.Language) > 64 || len(f.Tag) > 256 {
		return nil, ErrInvalid
	}
	if f.Difficulty != nil && (*f.Difficulty < 0 || *f.Difficulty > 10) {
		return nil, ErrInvalid
	}
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	query := "SELECT " + cardColumns + publicJoin
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		query += " AND " + fmt.Sprintf(clause, len(args))
	}
	if text := strings.TrimSpace(f.Query); text != "" {
		add("position(lower($%d) in lower((r.card->>'name') || ' ' || (r.card->>'author'))) > 0", text)
	}
	if f.Language != "" {
		add("r.card->>'language'=$%d", f.Language)
	}
	if f.Tag != "" {
		add("(r.card->'tags') ? $%d", f.Tag)
	}
	if f.Difficulty != nil {
		add("(r.card->>'difficulty')::int=$%d", *f.Difficulty)
	}
	if f.HasMedia != nil {
		add("(r.card->>'has_media')::boolean=$%d", *f.HasMedia)
	}
	if f.MaxBytes > 0 {
		add("r.size_bytes<=$%d", f.MaxBytes)
	}
	args = append(args, limit, f.Offset)
	query += fmt.Sprintf(" ORDER BY r.updated_at DESC,p.id LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	return listCards(ctx, tx, query, args...)
}
func (s *Service) Public(ctx context.Context, packID string) (Card, error) {
	return scanCard(s.pool.QueryRow(ctx, "SELECT "+cardColumns+publicJoin+" AND p.id=$1", packID))
}
func (s *Service) ForPlay(ctx context.Context, actor accounts.Identity, packID string, revision int) (Draft, error) {
	if actor.User.ID == "" {
		return Draft{}, ErrUnauthorized
	}
	return s.available(ctx, actor, packID, revision, false, false)
}
func (s *Service) ForDownload(ctx context.Context, actor accounts.Identity, packID string, revision int) (Draft, error) {
	return s.available(ctx, actor, packID, revision, true, false)
}

// Pinned is intentionally a trusted server primitive with no HTTP endpoint.
func (s *Service) Pinned(ctx context.Context, packID string, revision int) (Draft, error) {
	if revision <= 0 {
		return Draft{}, ErrInvalid
	}
	return s.available(ctx, accounts.Identity{}, packID, revision, false, true)
}
func (s *Service) available(ctx context.Context, actor accounts.Identity, packID string, revision int, download, pinned bool) (Draft, error) {
	if revision < 0 {
		return Draft{}, ErrInvalid
	}
	var a *access
	var err error
	if actor.User.ID != "" {
		scope := "drafts:read"
		if download {
			scope = "drafts:export"
		}
		a, err = s.begin(ctx, actor, scope, false, packID)
		if err != nil {
			return Draft{}, err
		}
	} else {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return Draft{}, err
		}
		a = &access{tx: tx}
		err = tx.QueryRow(ctx, `SELECT p.owner_id,p.current_revision,p.published_revision,p.pending_revision,p.blocked FROM library_packs p JOIN account_users u ON u.id=p.owner_id WHERE p.id=$1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL`, packID).Scan(&a.pack.owner, &a.pack.current, &a.pack.published, &a.pack.pending, &a.pack.blocked)
		if err != nil {
			a.close()
			if errors.Is(err, pgx.ErrNoRows) {
				return Draft{}, ErrNotFound
			}
			return Draft{}, err
		}
	}
	defer a.close()
	if a.pack.blocked {
		return Draft{}, ErrBlocked
	}
	owner := actor.User.ID != "" && a.pack.owner == actor.User.ID
	if !owner && !pinned {
		if a.pack.published == nil {
			return Draft{}, ErrNotFound
		}
		if revision == 0 {
			revision = *a.pack.published
		}
		if revision != *a.pack.published {
			return Draft{}, ErrNotFound
		}
	} else if revision == 0 {
		revision = a.pack.current
	}
	d, err := load(ctx, a.tx, packID, revision)
	if err != nil {
		return Draft{}, err
	}
	if download && !owner && !d.Card.AllowDownload {
		return Draft{}, ErrForbidden
	}
	if !packs.Playable(d.Issues) {
		return Draft{}, ErrInvalid
	}
	return d, nil
}
func (s *Service) Queue(ctx context.Context, actor accounts.Identity) ([]Card, error) {
	a, err := s.begin(ctx, actor, "", true, "")
	if err != nil {
		return nil, err
	}
	defer a.close()
	if !moderator(a.actor) {
		return nil, ErrForbidden
	}
	return listCards(ctx, a.tx, "SELECT "+cardColumns+` FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id AND r.revision=p.pending_revision JOIN account_users u ON u.id=p.owner_id WHERE p.deleted_at IS NULL AND NOT p.blocked AND u.deleted_at IS NULL ORDER BY r.updated_at,p.id LIMIT 200`)
}
func (s *Service) Decide(ctx context.Context, actor accounts.Identity, packID string, revision int, decision, reason string) (Card, error) {
	a, err := s.begin(ctx, actor, "", true, packID)
	if err != nil {
		return Card{}, err
	}
	defer a.close()
	if !moderator(a.actor) {
		return Card{}, ErrForbidden
	}
	if a.pack.blocked {
		return Card{}, ErrBlocked
	}
	if revision <= 0 || len(reason) > 8000 || !utf8.ValidString(reason) || strings.ContainsRune(reason, '\x00') {
		return Card{}, ErrInvalid
	}
	if decision != "approve" && decision != "reject" && decision != "block" {
		return Card{}, ErrInvalid
	}
	if decision != "approve" && !label(reason, 2000) {
		return Card{}, ErrInvalid
	}
	pending := a.pack.pending != nil && *a.pack.pending == revision
	published := a.pack.published != nil && *a.pack.published == revision
	if !pending && !(decision == "block" && published) {
		return Card{}, ErrConflict
	}
	d, err := load(ctx, a.tx, packID, revision)
	if err != nil {
		return Card{}, err
	}
	if decision == "approve" && (!packs.Playable(d.Issues) || !validRights(d.Card.Rights)) {
		return Card{}, ErrInvalid
	}
	switch decision {
	case "approve":
		if a.pack.published != nil {
			if _, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='withdrawn',updated_at=clock_timestamp() WHERE pack_id=$1 AND revision=$2", packID, *a.pack.published); err != nil {
				return Card{}, err
			}
		}
		if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET published_revision=$2,pending_revision=NULL WHERE id=$1", packID, revision); err != nil {
			return Card{}, err
		}
		_, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='published',updated_at=clock_timestamp() WHERE pack_id=$1 AND revision=$2", packID, revision)
	case "reject":
		if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET pending_revision=NULL WHERE id=$1", packID); err != nil {
			return Card{}, err
		}
		_, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='rejected',updated_at=clock_timestamp() WHERE pack_id=$1 AND revision=$2", packID, revision)
	case "block":
		if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET blocked=true,published_revision=NULL,pending_revision=NULL WHERE id=$1", packID); err != nil {
			return Card{}, err
		}
		_, err = a.tx.Exec(ctx, "UPDATE library_revisions SET status='blocked',updated_at=clock_timestamp() WHERE pack_id=$1 AND status IN ('pending','published')", packID)
	}
	if err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, "INSERT INTO library_decisions(pack_id,revision,moderator_id,decision,reason) VALUES($1,$2,$3,$4,$5)", packID, revision, a.actor.User.ID, decision, reason); err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_reports SET status='resolved' WHERE pack_id=$1 AND revision=$2", packID, revision); err != nil {
		return Card{}, err
	}
	d, err = load(ctx, a.tx, packID, revision)
	if err != nil {
		return Card{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Card{}, err
	}
	return d.Card, nil
}
func (s *Service) Report(ctx context.Context, actor accounts.Identity, packID string, revision int, reason string) (Report, error) {
	a, err := s.begin(ctx, actor, "", true, packID)
	if err != nil {
		return Report{}, err
	}
	defer a.close()
	if revision <= 0 || !label(reason, 2000) {
		return Report{}, ErrInvalid
	}
	if a.pack.blocked || a.pack.published == nil || *a.pack.published != revision {
		return Report{}, ErrNotFound
	}
	reportID, err := id()
	if err != nil {
		return Report{}, err
	}
	report := Report{ID: reportID, PackID: packID, Revision: revision, ReporterID: a.actor.User.ID, Reason: reason, Status: "open"}
	err = a.tx.QueryRow(ctx, `INSERT INTO library_reports(id,pack_id,revision,reporter_id,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT(pack_id,revision,reporter_id) DO NOTHING RETURNING created_at`, report.ID, packID, revision, report.ReporterID, reason).Scan(&report.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrConflict
	}
	if err != nil {
		return Report{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return report, nil
}
func (s *Service) Reports(ctx context.Context, actor accounts.Identity) ([]Report, error) {
	a, err := s.begin(ctx, actor, "", true, "")
	if err != nil {
		return nil, err
	}
	defer a.close()
	if !moderator(a.actor) {
		return nil, ErrForbidden
	}
	rows, err := a.tx.Query(ctx, "SELECT id,pack_id,revision,reporter_id,reason,status,created_at FROM library_reports ORDER BY (status='open') DESC,created_at,id LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []Report{}
	for rows.Next() {
		var r Report
		if err = rows.Scan(&r.ID, &r.PackID, &r.Revision, &r.ReporterID, &r.Reason, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}
