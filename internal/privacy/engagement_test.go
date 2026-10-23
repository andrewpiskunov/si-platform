package privacy

import (
	"bytes"
	"context"
	"siplatform/examples"
	"siplatform/internal/library"
	"strings"
	"testing"
)

func TestExportOwnVotesOnly(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('other','other','Other','secret')`)
	if e != nil {
		t.Fatal(e)
	}
	d, e := library.New(p).Create(ctx, a, library.Save{Document: examples.Demo()})
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO package_votes(pack_id,user_id,rating,liked) VALUES($2,$1,4,true),($2,'other',1,false)`, a.User.ID, d.Card.ID)
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e = s.Export(ctx, a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	v := unpack(t, b.Bytes())["package_votes.ndjson"]
	if !strings.Contains(v, `"rating": 4`) || strings.Contains(v, `"rating": 1`) || strings.Contains(v, `"user_id": "other"`) {
		t.Fatal(v)
	}
}
