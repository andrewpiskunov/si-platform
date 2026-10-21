package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/packs"
	"siplatform/internal/playpack"
)

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type packState struct {
	owner              string
	current            int
	published, pending *int
	blocked            bool
}
type access struct {
	tx    pgx.Tx
	actor accounts.Identity
	pack  packState
}

func id() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func moderator(a accounts.Identity) bool {
	return !a.APIToken && (a.User.Role == "moderator" || a.User.Role == "admin")
}

// Lock account rows in a deterministic order before the pack. This coordinates
// with account deletion and avoids deadlocks when moderators review each other.
func (s *Service) begin(ctx context.Context, a accounts.Identity, scope string, session bool, packID string) (*access, error) {
	if a.User.ID == "" || a.SessionID == "" {
		return nil, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	out := &access{tx: tx, actor: a}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(context.Background())
		}
	}()
	ids := []string{a.User.ID}
	if packID != "" {
		err = tx.QueryRow(ctx, "SELECT owner_id FROM library_packs WHERE id=$1 AND deleted_at IS NULL", packID).Scan(&out.pack.owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, out.pack.owner)
	}
	rows, err := tx.Query(ctx, "SELECT id,display_name,role FROM account_users WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE", ids)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for rows.Next() {
		var user accounts.User
		if err = rows.Scan(&user.ID, &user.DisplayName, &user.Role); err != nil {
			rows.Close()
			return nil, err
		}
		active[user.ID] = true
		if user.ID == a.User.ID {
			out.actor.User = user
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !active[a.User.ID] {
		return nil, ErrUnauthorized
	}
	if packID != "" && !active[out.pack.owner] {
		return nil, ErrNotFound
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind,scopes FROM account_credentials WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, a.SessionID, a.User.ID).Scan(&kind, &out.actor.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	out.actor.APIToken = kind == "api"
	if out.actor.APIToken {
		if session {
			return nil, ErrForbidden
		}
		found := false
		for _, value := range out.actor.Scopes {
			if value == scope {
				found = true
			}
		}
		if !found {
			return nil, ErrForbidden
		}
	}
	if packID != "" {
		err = tx.QueryRow(ctx, `SELECT owner_id,current_revision,published_revision,pending_revision,blocked FROM library_packs WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, packID).Scan(&out.pack.owner, &out.pack.current, &out.pack.published, &out.pack.pending, &out.pack.blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	ok = true
	return out, nil
}
func (a *access) close() { _ = a.tx.Rollback(context.Background()) }
func (a *access) owner() error {
	if a.actor.User.ID != a.pack.owner {
		return ErrNotFound
	}
	return nil
}
func (a *access) reader() error {
	if a.actor.User.ID != a.pack.owner && !moderator(a.actor) {
		return ErrNotFound
	}
	return nil
}

const cardColumns = "r.card,r.status,r.rights,r.updated_at,p.blocked,COALESCE(p.published_revision,0),COALESCE(p.pending_revision,0)"

type scanner interface{ Scan(...any) error }

func scanCard(row scanner) (Card, error) {
	var card Card
	var metadata, rights []byte
	var status string
	var blocked bool
	var published, pending int
	err := row.Scan(&metadata, &status, &rights, &card.UpdatedAt, &blocked, &published, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return card, ErrNotFound
	}
	if err != nil {
		return card, err
	}
	updated := card.UpdatedAt
	if err = json.Unmarshal(metadata, &card); err != nil {
		return card, err
	}
	if err = json.Unmarshal(rights, &card.Rights); err != nil {
		return card, err
	}
	card.Status = status
	card.PublishedRevision = published
	card.PendingRevision = pending
	card.UpdatedAt = updated
	card.AllowDownload = card.Rights.AllowDownload
	if blocked {
		card.Status = "blocked"
	}
	if card.Tags == nil {
		card.Tags = []string{}
	}
	return card, nil
}
func load(ctx context.Context, tx pgx.Tx, packID string, revision int) (Draft, error) {
	var d Draft
	var doc, assets, issues, assetIssues []byte
	card, err := scanCard(tx.QueryRow(ctx, "SELECT "+cardColumns+" FROM library_revisions r JOIN library_packs p ON p.id=r.pack_id JOIN account_users u ON u.id=p.owner_id WHERE p.id=$1 AND r.revision=$2 AND p.deleted_at IS NULL AND u.deleted_at IS NULL", packID, revision))
	if err != nil {
		return d, err
	}
	d.Card = card
	err = tx.QueryRow(ctx, "SELECT document,assets,original_key,issues,asset_issues FROM library_revisions WHERE pack_id=$1 AND revision=$2", packID, revision).Scan(&doc, &assets, &d.OriginalKey, &issues, &assetIssues)
	if err != nil {
		return d, err
	}
	for _, v := range []struct {
		data []byte
		dest any
	}{{doc, &d.Document}, {assets, &d.Assets}, {issues, &d.Issues}, {assetIssues, &d.AssetIssues}} {
		if err = json.Unmarshal(v.data, v.dest); err != nil {
			return d, err
		}
	}
	refreshMediaDiagnostics(&d)
	d.PlayIssues = playpack.ValidateMedia(d.Document, MediaLookup(d.Assets))
	return d, nil
}
func listCards(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]Card, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Card{}
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, card)
	}
	return result, rows.Err()
}
func (s *Service) Create(ctx context.Context, actor accounts.Identity, in Save) (Draft, error) {
	a, err := s.begin(ctx, actor, "drafts:write", false, "")
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	result, err := createInAccess(ctx, a, in)
	if err != nil {
		return Draft{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Draft{}, err
	}
	return result, nil
}

// createInAccess preserves creation invariants inside the caller's account lock.
// The caller owns commit, allowing additional receipts to be atomic with creation.
func createInAccess(ctx context.Context, a *access, in Save) (Draft, error) {
	catalogue, err := catalogueForCreate(ctx, a, in.ImportID)
	if err != nil {
		return Draft{}, err
	}
	if catalogue.pack != "" {
		var revision int
		var blocked bool
		e := a.tx.QueryRow(ctx, `SELECT current_revision,blocked FROM library_packs WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL FOR UPDATE`, catalogue.pack, a.actor.User.ID).Scan(&revision, &blocked)
		if errors.Is(e, pgx.ErrNoRows) {
			return Draft{}, ErrNotFound
		}
		if e != nil {
			return Draft{}, e
		}
		if blocked {
			return Draft{}, ErrBlocked
		}
		return load(ctx, a.tx, catalogue.pack, revision)
	}

	if in.ImportID != "" {
		if !hexID(in.ImportID, 32) {
			return Draft{}, ErrInvalid
		}
		var existing string
		var revision int
		err = a.tx.QueryRow(ctx, "SELECT id,current_revision FROM library_packs WHERE owner_id=$1 AND import_id=$2 AND deleted_at IS NULL", a.actor.User.ID, in.ImportID).Scan(&existing, &revision)
		if err == nil {
			return load(ctx, a.tx, existing, revision)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Draft{}, err
		}
		// A deleted import cannot be recreated by a delayed worker retry.
		var deleted bool
		if err = a.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_packs WHERE owner_id=$1 AND import_id=$2)", a.actor.User.ID, in.ImportID).Scan(&deleted); err != nil {
			return Draft{}, err
		}
		if deleted {
			return Draft{}, ErrNotFound
		}
	}
	d, err := prepare(ctx, in)
	if err != nil {
		return Draft{}, err
	}
	var count int
	var used int64
	err = a.tx.QueryRow(ctx, `SELECT count(*) FROM library_packs WHERE owner_id=$1 AND deleted_at IS NULL`, a.actor.User.ID).Scan(&count)
	if err != nil {
		return Draft{}, err
	}
	if count >= PackCountLimit {
		return Draft{}, ErrQuota
	}
	used, err = usage(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Draft{}, err
	}
	if used+d.Card.SizeBytes > StorageLimitBytes {
		return Draft{}, ErrQuota
	}
	packID, err := id()
	if err != nil {
		return Draft{}, err
	}
	d.Card.ID = packID
	d.Card.OwnerID = a.actor.User.ID
	d.Card.Revision = 1
	if d.Card.Author == "" {
		d.Card.Author = a.actor.User.DisplayName
	}
	var importID any
	if in.ImportID != "" {
		importID = in.ImportID
	}
	_, err = a.tx.Exec(ctx, "INSERT INTO library_packs(id,owner_id,current_revision,import_id) VALUES($1,$2,1,$3)", packID, a.actor.User.ID, importID)
	if err != nil {
		return Draft{}, err
	}
	if err = insert(ctx, a.tx, d); err != nil {
		return Draft{}, err
	}
	if err = catalogueRecord(ctx, a, catalogue, packID); err != nil {
		return Draft{}, err
	}
	result, err := load(ctx, a.tx, packID, 1)
	if err != nil {
		return Draft{}, err
	}
	return result, nil
}
func usage(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	return StorageUsage(ctx, tx, userID)
}

// StorageUsage counts immutable pack revisions, retained theme versions, and
// ready uploads awaiting attachment. Ordinary theme deletion retains its charge.
// Quota callers must hold the account row lock for their transaction.
func StorageUsage(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT
 COALESCE((SELECT sum(r.size_bytes) FROM library_revisions r JOIN library_packs p ON p.id=r.pack_id WHERE p.owner_id=$1 AND p.deleted_at IS NULL),0)::bigint
 + COALESCE((SELECT sum(COALESCE((u.result_asset->>'size')::bigint,0)
     + COALESCE((u.result_asset->'prepared'->'standard'->>'size')::bigint,0)
     + COALESCE((u.result_asset->'prepared'->'economy'->>'size')::bigint,0))
   FROM uploads u WHERE u.owner_id=$1 AND u.purpose='media' AND u.status='ready'
   AND NOT EXISTS(SELECT 1 FROM library_media_receipts receipt JOIN library_packs p ON p.id=receipt.pack_id WHERE receipt.upload_id=u.id AND p.deleted_at IS NULL)),0)::bigint
 + COALESCE((SELECT sum(v.size_bytes) FROM theme_versions v JOIN themes t ON t.id=v.theme_id WHERE t.owner_id=$1),0)::bigint
 + COALESCE((SELECT sum((u.result_theme->>'size_bytes')::bigint) FROM uploads u
   WHERE u.owner_id=$1 AND u.purpose='theme' AND u.status='ready'
   AND NOT EXISTS(SELECT 1 FROM theme_receipts receipt WHERE receipt.upload_id=u.id)),0)::bigint`, userID).Scan(&n)
	return n, err
}
func insert(ctx context.Context, tx pgx.Tx, d Draft) error {
	values := make([][]byte, 5)
	for i, v := range []any{d.Card, d.Document, d.Assets, d.Issues, d.AssetIssues} {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		values[i] = b
	}
	searchText, ageRating := catalogueMetadata(d.Card, d.Document)
	_, err := tx.Exec(ctx, `INSERT INTO library_revisions(pack_id,revision,card,document,assets,original_key,issues,size_bytes,asset_issues,search_text,age_rating) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, d.Card.ID, d.Card.Revision, values[0], values[1], values[2], d.OriginalKey, values[3], d.Card.SizeBytes, values[4], searchText, ageRating)
	return err
}
func (s *Service) Update(ctx context.Context, actor accounts.Identity, packID string, base int, in Save) (Draft, error) {
	return s.update(ctx, actor, packID, base, in, false)
}

// UpdateDocument changes editable content without requiring read scope. Media
// ownership and importer diagnostics come from the locked current revision.
func (s *Service) UpdateDocument(ctx context.Context, actor accounts.Identity, packID string, base int, doc packs.Document) (Draft, error) {
	return s.update(ctx, actor, packID, base, Save{Document: doc}, true)
}

func (s *Service) update(ctx context.Context, actor accounts.Identity, packID string, base int, in Save, documentOnly bool) (Draft, error) {
	a, err := s.begin(ctx, actor, "drafts:write", false, packID)
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	result, err := updateInAccess(ctx, a, packID, base, in, documentOnly)
	if err != nil {
		return Draft{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Draft{}, err
	}
	return result, nil
}

// updateInAccess keeps revision, quota and asset invariants in the caller's transaction.
func updateInAccess(ctx context.Context, a *access, packID string, base int, in Save, documentOnly bool) (Draft, error) {
	var err error
	if err = a.owner(); err != nil {
		return Draft{}, err
	}
	if a.pack.blocked {
		return Draft{}, ErrBlocked
	}
	if base != a.pack.current {
		return Draft{}, ErrConflict
	}
	if base >= PackRevisionLimit {
		return Draft{}, ErrQuota
	}
	if in.ImportID != "" {
		return Draft{}, ErrInvalid
	}
	if documentOnly {
		prior, err := load(ctx, a.tx, packID, a.pack.current)
		if err != nil {
			return Draft{}, err
		}
		in.Document.ImportIssues = prior.Document.ImportIssues
		in.Assets = prior.Assets
		in.OriginalKey = prior.OriginalKey
		in.Issues = prior.AssetIssues
	}
	d, err := prepare(ctx, in)
	if err != nil {
		return Draft{}, err
	}
	used, err := usage(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Draft{}, err
	}
	if used+d.Card.SizeBytes > StorageLimitBytes {
		return Draft{}, ErrQuota
	}
	d.Card.ID = packID
	d.Card.OwnerID = a.actor.User.ID
	d.Card.Revision = base + 1
	if d.Card.Author == "" {
		d.Card.Author = a.actor.User.DisplayName
	}
	if err = insert(ctx, a.tx, d); err != nil {
		return Draft{}, err
	}
	if _, err = a.tx.Exec(ctx, "UPDATE library_packs SET current_revision=$2 WHERE id=$1", packID, base+1); err != nil {
		return Draft{}, err
	}
	result, err := load(ctx, a.tx, packID, base+1)
	if err != nil {
		return Draft{}, err
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, actor accounts.Identity, packID string, revision int) (Draft, error) {
	a, err := s.begin(ctx, actor, "drafts:read", false, packID)
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	if err = a.reader(); err != nil {
		return Draft{}, err
	}
	if revision < 0 {
		return Draft{}, ErrInvalid
	}
	if revision == 0 {
		revision = a.pack.current
	}
	return load(ctx, a.tx, packID, revision)
}
func (s *Service) Mine(ctx context.Context, actor accounts.Identity) ([]Card, error) {
	a, err := s.begin(ctx, actor, "drafts:read", false, "")
	if err != nil {
		return nil, err
	}
	defer a.close()
	return listCards(ctx, a.tx, "SELECT "+cardColumns+" FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id AND r.revision=p.current_revision WHERE p.owner_id=$1 AND p.deleted_at IS NULL ORDER BY r.updated_at DESC,p.id LIMIT 200", a.actor.User.ID)
}
func (s *Service) History(ctx context.Context, actor accounts.Identity, packID string) ([]Card, error) {
	a, err := s.begin(ctx, actor, "drafts:read", false, packID)
	if err != nil {
		return nil, err
	}
	defer a.close()
	if err = a.reader(); err != nil {
		return nil, err
	}
	return listCards(ctx, a.tx, "SELECT "+cardColumns+" FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id WHERE p.id=$1 ORDER BY r.revision DESC", packID)
}
func (s *Service) Delete(ctx context.Context, actor accounts.Identity, packID string) error {
	a, err := s.begin(ctx, actor, "drafts:write", false, packID)
	if err != nil {
		return err
	}
	defer a.close()
	if err = a.owner(); err != nil {
		return err
	}
	_, err = a.tx.Exec(ctx, "UPDATE library_packs SET deleted_at=clock_timestamp(),published_revision=NULL,pending_revision=NULL WHERE id=$1", packID)
	if err != nil {
		return err
	}
	return a.tx.Commit(ctx)
}

// Keep error wrapping descriptive for internal failures while sentinel checks remain stable.
func invalid(what string) error { return fmt.Errorf("%w: %s", ErrInvalid, what) }

// ForFile authorizes private asset or original-archive access independently of
// SIQ validity. Blocking stops file delivery while Get keeps diagnostics readable.
func (s *Service) ForFile(ctx context.Context, actor accounts.Identity, packID string, revision int, original bool) (Draft, error) {
	scope := "drafts:read"
	if original {
		scope = "drafts:export"
	}
	a, err := s.begin(ctx, actor, scope, false, packID)
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	if err = a.reader(); err != nil {
		return Draft{}, err
	}
	if a.pack.blocked {
		return Draft{}, ErrBlocked
	}
	if revision < 0 {
		return Draft{}, ErrInvalid
	}
	if revision == 0 {
		revision = a.pack.current
	}
	return load(ctx, a.tx, packID, revision)
}
