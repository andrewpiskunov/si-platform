package retention

import (
	"context"
	"strings"
	"testing"
)

func TestMCPReceiptRetentionHonorsOwnerLease(t *testing.T) {
	s, p, _ := fixture(t)
	ctx := context.Background()
	exec(t, p, `INSERT INTO mcp_receipts(owner_id,idempotency_key,operation,request_hash,pack_id,result_revision) SELECT 'a','receipt-key-'||lpad(n::text,8,'0'),'create_draft','hash','purged-pack',1 FROM generate_series(1,30)n; UPDATE account_users SET deleted_at=now()-interval '31 days',username=NULL,password_hash=NULL WHERE id='a'; INSERT INTO account_export_leases VALUES('a',$1,now()+interval '1 hour')`, strings.Repeat("9", 32))
	if _, e := s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM mcp_receipts`).Scan(&n)
	if n != 30 {
		t.Fatal("lease ignored", n)
	}
	exec(t, p, `DELETE FROM account_export_leases`)
	if _, e := s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	p.QueryRow(ctx, `SELECT count(*) FROM mcp_receipts`).Scan(&n)
	if n != 5 {
		t.Fatal("bounded cleanup missing", n)
	}
}
