package privacy

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExportMCPReceiptsOwnAllowlist(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, `INSERT INTO mcp_receipts(owner_id,idempotency_key,operation,request_hash,pack_id,result_revision) VALUES($1,'own-request-key-01','create_draft','hash','purged-pack',1)`, a.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('other','other','Other','secret'); INSERT INTO mcp_receipts(owner_id,idempotency_key,operation,request_hash,pack_id,result_revision) VALUES('other','foreign-request-01','create_draft','hash','foreign',1)`); e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e := s.Export(ctx, a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	v := unpack(t, b.Bytes())["mcp_receipts.ndjson"]
	if !strings.Contains(v, "own-request-key-01") || strings.Contains(v, "foreign") || strings.Contains(v, "request_hash") {
		t.Fatal("missing or unsafe receipt export", v)
	}
}
