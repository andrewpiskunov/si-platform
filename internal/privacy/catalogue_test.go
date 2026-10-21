package privacy

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExportCatalogueOwnAllowlist(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO catalogue_imports(id,owner_id,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id) VALUES('receipt',$1,'pilot','own-item','secret-hash','sha','file.siq',10,'https://example.org','CC0','Author','own-work','upload')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e = s.Export(ctx, a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	v := unpack(t, b.Bytes())["catalogue_imports.ndjson"]
	if !strings.Contains(v, "own-item") || !strings.Contains(v, "own-work") || strings.Contains(v, "secret-hash") || strings.Contains(v, "request_hash") {
		t.Fatal(v)
	}
}
