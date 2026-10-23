package ingest

import (
	"context"
	"os"
	"regexp"
	"siplatform/internal/accounts"
)

var externalIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// ExternalInput records provenance, without asserting a licence or permission.
type ExternalInput struct {
	ExternalID  string `json:"external_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Attribution string `json:"attribution"`
}

func (in ExternalInput) catalogue() CatalogueInput {
	return CatalogueInput{Namespace: "sibrowser", ExternalID: in.ExternalID, Name: in.Name, Size: in.Size, SHA256: in.SHA256, Attribution: in.Attribution, SourceURL: "https://www.sibrowser.ru/packages/" + in.ExternalID, Origin: "external_import"}
}
func (in ExternalInput) Validate() error {
	if !externalIDPattern.MatchString(in.ExternalID) {
		return ErrInvalid
	}
	c := in.catalogue()
	// Validate the common fields without manufacturing persisted rights metadata.
	c.License = "validation"
	c.PermissionBasis = "validation"
	return c.Validate()
}
func (s *Service) CreateExternal(ctx context.Context, a accounts.Identity, in ExternalInput) (Upload, error) {
	if err := in.Validate(); err != nil {
		return Upload{}, err
	}
	c := in.catalogue()
	return s.create(ctx, a, in.Name, in.Size, "siq", "", &c)
}

// RetryExternal restarts a failed upload under the original receipt. It cannot
// reset an import that has already produced a pack (even a deleted pack).
func (s *Service) RetryExternal(ctx context.Context, a accounts.Identity, in ExternalInput) (Upload, error) {
	if e := in.Validate(); e != nil {
		return Upload{}, e
	}
	tx, e := s.begin(ctx, a, true)
	if e != nil {
		return Upload{}, e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_database()||':'||current_schema()||':si-ingest-quota',0))`); e != nil {
		return Upload{}, safeError(ctx, e)
	}
	u, found, e := catalogueReplay(ctx, tx, a.User.ID, in.catalogue())
	if e != nil {
		return Upload{}, e
	}
	if !found {
		return Upload{}, ErrNotFound
	}
	if u.Status != "failed" {
		return u, nil
	}
	var pack string
	if e = tx.QueryRow(ctx, `SELECT pack_id FROM catalogue_imports WHERE upload_id=$1`, u.ID).Scan(&pack); e != nil {
		return Upload{}, safeError(ctx, e)
	}
	if pack != "" {
		return Upload{}, ErrConflict
	}
	var total, own int
	var storage int64
	if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE owner_id=$1),coalesce(sum(size),0) FROM uploads WHERE status IN ('uploading','queued','processing')`, a.User.ID).Scan(&total, &own, &storage); e != nil {
		return Upload{}, safeError(ctx, e)
	}
	if total >= 16 || own >= 2 || storage+in.Size > 4<<30 {
		return Upload{}, ErrQuota
	}
	// The row lock coordinates with terminal stage cleanup. Preserve staged bytes
	// on an uncertain commit; Append always truncates to the committed offset.
	f, e := s.root.OpenFile(u.ID, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if e != nil {
		return Upload{}, ErrUnavailable
	}
	e = f.Sync()
	ce := f.Close()
	if e != nil || ce != nil {
		return Upload{}, ErrUnavailable
	}
	u, e = scanUpload(tx.QueryRow(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(id) DO UPDATE SET credential_id=EXCLUDED.credential_id,status='uploading',offset_bytes=0,error='',lease_epoch=0,staged_deleted=false,expires_at=now()+interval '24 hours',updated_at=now() WHERE uploads.status='failed' AND uploads.draft_id='' RETURNING `+uploadColumns, u.ID, a.User.ID, a.SessionID, in.Name, in.Size))
	if e != nil {
		return Upload{}, safeError(ctx, e)
	}
	if e = tx.Commit(ctx); e != nil {
		return Upload{}, safeError(ctx, e)
	}
	return u, nil
}
