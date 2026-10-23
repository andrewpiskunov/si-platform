package library

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/packs"
)

// PublishExternal is deliberately separate from user confirmation/moderation.
// The live source owner may publish only the first, unedited imported revision.
func (s *Service) PublishExternal(ctx context.Context, actor accounts.Identity, source, externalID string) (Card, error) {
	var pack string
	err := s.pool.QueryRow(ctx, `SELECT pack_id FROM catalogue_imports WHERE namespace=$1 AND external_id=$2 AND origin='external_import' AND owner_id=$3`, source, externalID, actor.User.ID).Scan(&pack)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pack == "" {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, err
	}
	a, err := s.begin(ctx, actor, "drafts:write", false, pack)
	if err != nil {
		return Card{}, err
	}
	defer a.close()
	if a.actor.User.Role != "admin" {
		return Card{}, ErrForbidden
	}
	var allowed bool
	if err = a.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM external_sources WHERE source=$1 AND owner_id=$2)`, source, a.actor.User.ID).Scan(&allowed); err != nil {
		return Card{}, err
	}
	if !allowed {
		return Card{}, ErrForbidden
	}
	if err = a.owner(); err != nil {
		return Card{}, err
	}
	if a.pack.blocked {
		return Card{}, ErrBlocked
	}
	var once bool
	if err = a.tx.QueryRow(ctx, `SELECT published_once FROM catalogue_imports WHERE namespace=$1 AND external_id=$2 AND origin='external_import' AND pack_id=$3 FOR UPDATE`, source, externalID, pack).Scan(&once); err != nil {
		return Card{}, err
	}
	if a.pack.current != 1 || a.pack.pending != nil {
		return Card{}, ErrConflict
	}
	d, err := load(ctx, a.tx, pack, 1)
	if err != nil {
		return Card{}, err
	}
	if once {
		if a.pack.published == nil || *a.pack.published != 1 {
			return Card{}, ErrConflict
		}
		return d.Card, nil
	}
	if a.pack.published != nil || d.Card.Status != "draft" {
		return Card{}, ErrConflict
	}
	if !packs.Playable(d.Issues) || !packs.Playable(d.PlayIssues) {
		return Card{}, ErrInvalid
	}
	if _, err = a.tx.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, pack); err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, `UPDATE library_revisions SET status='published',updated_at=clock_timestamp(),card=jsonb_set(card,'{author}',to_jsonb($2::text)) WHERE pack_id=$1 AND revision=1`, pack, d.Card.Rights.Attribution); err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, `UPDATE catalogue_imports SET published_once=true WHERE namespace=$1 AND external_id=$2 AND origin='external_import'`, source, externalID); err != nil {
		return Card{}, err
	}
	if _, err = a.tx.Exec(ctx, `INSERT INTO library_decisions(pack_id,revision,moderator_id,decision,reason) VALUES($1,1,$2,'approve','Automatic external import: sibrowser')`, pack, a.actor.User.ID); err != nil {
		return Card{}, err
	}
	d, err = load(ctx, a.tx, pack, 1)
	if err != nil {
		return Card{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Card{}, err
	}
	return d.Card, nil
}

// AccountLimits is shared by writers and usage reporting under the account lock.
func AccountLimits(ctx context.Context, tx pgx.Tx, owner string) (int, int64, error) {
	count, bytes := PackCountLimit, StorageLimitBytes
	err := tx.QueryRow(ctx, `SELECT pack_limit,storage_limit FROM external_sources WHERE owner_id=$1`, owner).Scan(&count, &bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return PackCountLimit, StorageLimitBytes, nil
	}
	return count, bytes, err
}
