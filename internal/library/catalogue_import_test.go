package library

import (
	"errors"
	"testing"
)

func TestCatalogueReceiptPreventsResurrectionAndRechecksRole(t *testing.T) {
	for _, mode := range []string{"purged", "blocked", "role", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			s, p := setup(t)
			a := identity(t, p, "owner", "admin", "api", "drafts:write")
			const upload = "0123456789abcdef0123456789abcdef"
			_, e := p.Exec(ctx, `INSERT INTO catalogue_imports(id,owner_id,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id) VALUES('receipt','owner','pilot','one','hash','sha','file.siq',10,'https://example.org','CC0','Author','own-work',$1)`, upload)
			if e != nil {
				t.Fatal(e)
			}
			input := Save{ImportID: upload, Document: document("Pilot")}
			d, e := s.Create(ctx, a, input)
			if e != nil {
				t.Fatal(e)
			}
			want := ErrNotFound
			switch mode {
			case "purged":
				tx, err := p.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, e = tx.Exec(ctx, `DELETE FROM library_revisions WHERE pack_id=$1`, d.Card.ID); e == nil {
					_, e = tx.Exec(ctx, `DELETE FROM library_packs WHERE id=$1`, d.Card.ID)
				}
				if e == nil {
					e = tx.Commit(ctx)
				}
			case "blocked":
				_, e = p.Exec(ctx, `UPDATE library_packs SET blocked=true WHERE id=$1`, d.Card.ID)
				want = ErrBlocked
			case "role":
				_, e = p.Exec(ctx, `UPDATE account_users SET role='author' WHERE id='owner'`)
				want = ErrForbidden
			case "revoked":
				_, e = p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now() WHERE id=$1`, a.SessionID)
				want = ErrUnauthorized
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Create(ctx, a, input); !errors.Is(e, want) {
				t.Fatal(e, want)
			}
		})
	}
}
