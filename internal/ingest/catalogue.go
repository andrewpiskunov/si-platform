package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
)

const CatalogueReceiptLimit = 100000

type CatalogueInput struct {
	Namespace       string `json:"namespace"`
	ExternalID      string `json:"external_id"`
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	SHA256          string `json:"sha256"`
	SourceURL       string `json:"source_url"`
	License         string `json:"license"`
	Attribution     string `json:"attribution"`
	PermissionBasis string `json:"permission_basis"`
}

func (in CatalogueInput) Validate() error {
	for _, v := range []struct {
		s string
		n int
	}{{in.Namespace, 128}, {in.ExternalID, 200}, {in.SourceURL, 2048}, {in.License, 200}, {in.Attribution, 1000}, {in.PermissionBasis, 2000}} {
		if strings.TrimSpace(v.s) == "" || len(v.s) > v.n || !utf8.ValidString(v.s) {
			return ErrInvalid
		}
		for _, r := range v.s {
			if unicode.IsControl(r) {
				return ErrInvalid
			}
		}
	}
	u, e := url.Parse(in.SourceURL)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return ErrInvalid
	}
	hash, e := hex.DecodeString(in.SHA256)
	if e != nil || len(hash) != 32 || strings.ToLower(in.SHA256) != in.SHA256 || !validName(in.Name) || in.Size <= 0 || in.Size > maxUpload {
		return ErrInvalid
	}
	return nil
}
func (s *Service) CreateCatalogue(ctx context.Context, a accounts.Identity, in CatalogueInput) (Upload, error) {
	if e := in.Validate(); e != nil {
		return Upload{}, e
	}
	return s.create(ctx, a, in.Name, in.Size, "siq", "", &in)
}
func catalogueAdmin(ctx context.Context, tx pgx.Tx, owner string) error {
	var role string
	if e := tx.QueryRow(ctx, `SELECT role FROM account_users WHERE id=$1 AND deleted_at IS NULL`, owner).Scan(&role); e != nil {
		return safeError(ctx, e)
	}
	if role != "admin" {
		return ErrForbidden
	}
	return nil
}

// Called under the existing live-credential/account lock, before quota or file creation.
func catalogueReplay(ctx context.Context, tx pgx.Tx, owner string, in CatalogueInput) (Upload, bool, error) {
	if e := catalogueAdmin(ctx, tx, owner); e != nil {
		return Upload{}, false, e
	}
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	var hash, upload, pack string
	e := tx.QueryRow(ctx, `SELECT request_hash,upload_id,pack_id FROM catalogue_imports WHERE owner_id=$1 AND namespace=$2 AND external_id=$3`, owner, in.Namespace, in.ExternalID).Scan(&hash, &upload, &pack)
	if errors.Is(e, pgx.ErrNoRows) {
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM catalogue_imports WHERE owner_id=$1`, owner).Scan(&n); e != nil {
			return Upload{}, false, safeError(ctx, e)
		}
		if n >= CatalogueReceiptLimit {
			return Upload{}, false, ErrQuota
		}
		return Upload{}, false, nil
	}
	if e != nil {
		return Upload{}, false, safeError(ctx, e)
	}
	if hash != hex.EncodeToString(sum[:]) {
		return Upload{}, false, ErrConflict
	}
	if pack != "" {
		var blocked bool
		e = tx.QueryRow(ctx, `SELECT blocked FROM library_packs WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL`, pack, owner).Scan(&blocked)
		if errors.Is(e, pgx.ErrNoRows) {
			return Upload{}, false, ErrNotFound
		}
		if e != nil {
			return Upload{}, false, safeError(ctx, e)
		}
		if blocked {
			return Upload{}, false, ErrForbidden
		}
		return Upload{ID: upload, Size: in.Size, Offset: in.Size, Status: "ready", DraftID: pack, Purpose: "siq"}, true, nil
	}
	u, e := owned(ctx, tx, owner, upload)
	if errors.Is(e, ErrNotFound) {
		return Upload{ID: upload, Size: in.Size, Status: "failed", Error: "expired", Purpose: "siq"}, true, nil
	}
	return u, true, e
}
func catalogueInsert(ctx context.Context, tx pgx.Tx, owner, upload string, in CatalogueInput) error {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	id, e := randomID()
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO catalogue_imports(id,owner_id,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, id, owner, in.Namespace, in.ExternalID, hex.EncodeToString(sum[:]), in.SHA256, in.Name, in.Size, in.SourceURL, in.License, in.Attribution, in.PermissionBasis, upload)
	return safeError(ctx, e)
}
