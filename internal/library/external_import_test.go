package library

import (
	"errors"
	"testing"
)

func TestSourceQuotasApplyOnlyToConfiguredOwner(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "source", "admin", "session")
	b := identity(t, p, "other", "author", "session")
	if _, e := p.Exec(ctx, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit) VALUES('sibrowser','source',1,1024)`); e != nil {
		t.Fatal(e)
	}
	d := create(t, s, a, "One")
	if _, e := s.Create(ctx, a, Save{Document: document("Two")}); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	u, e := s.Usage(ctx, a)
	if e != nil || u.Packs.Limit != 1 || u.Storage.LimitBytes != 1024 {
		t.Fatal(u, e)
	}
	if _, e = p.Exec(ctx, `UPDATE external_sources SET pack_limit=1000,storage_limit=20 WHERE source='sibrowser'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.UpdateDocument(ctx, a, d.Card.ID, 1, document("Changed")); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	u, e = s.Usage(ctx, b)
	if e != nil || u.Packs.Limit != 200 || u.Storage.LimitBytes != 10<<30 {
		t.Fatal(u, e)
	}
}
