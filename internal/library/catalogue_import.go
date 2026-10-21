package library

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type catalogueImport struct {
	id, pack string
	rights   Rights
}

func catalogueForCreate(ctx context.Context, a *access, upload string) (catalogueImport, error) {
	var c catalogueImport
	if upload == "" {
		return c, nil
	}
	e := a.tx.QueryRow(ctx, `SELECT id,pack_id,license,source_url,attribution FROM catalogue_imports WHERE upload_id=$1 AND owner_id=$2`, upload, a.actor.User.ID).Scan(&c.id, &c.pack, &c.rights.License, &c.rights.SourceURL, &c.rights.Attribution)
	if errors.Is(e, pgx.ErrNoRows) {
		return catalogueImport{}, nil
	}
	if e != nil {
		return c, e
	}
	if a.actor.User.Role != "admin" {
		return c, ErrForbidden
	}
	return c, nil
}
func catalogueRecord(ctx context.Context, a *access, c catalogueImport, pack string) error {
	if c.id == "" {
		return nil
	}
	raw, e := json.Marshal(c.rights)
	if e != nil {
		return e
	}
	if _, e = a.tx.Exec(ctx, `UPDATE library_revisions SET rights=$2 WHERE pack_id=$1 AND revision=1`, pack, raw); e != nil {
		return e
	}
	_, e = a.tx.Exec(ctx, `UPDATE catalogue_imports SET pack_id=$2 WHERE id=$1 AND pack_id=''`, c.id, pack)
	return e
}
