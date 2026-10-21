package retention

import (
	"context"
	"strings"
	"testing"
)

func TestCatalogueRetentionHonorsLease(t *testing.T) {
	s, p, _ := fixture(t)
	ctx := context.Background()
	exec(t, p, `INSERT INTO catalogue_imports(id,owner_id,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id) SELECT n::text,'a','pilot',n::text,'hash','sha','file.siq',10,'https://example.org','CC0','Author','own-work',n::text FROM generate_series(1,30)n; UPDATE account_users SET deleted_at=now()-interval '31 days',username=NULL,password_hash=NULL WHERE id='a'; INSERT INTO account_export_leases VALUES('a',$1,now()+interval '1 hour')`, strings.Repeat("9", 32))
	if _, e := s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM catalogue_imports`).Scan(&n); e != nil || n != 30 {
		t.Fatal(n, e)
	}
	exec(t, p, `DELETE FROM account_export_leases`)
	if _, e := s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if e := p.QueryRow(ctx, `SELECT count(*) FROM catalogue_imports`).Scan(&n); e != nil || n != 5 {
		t.Fatal(n, e)
	}
}
