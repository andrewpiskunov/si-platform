package library

import (
	"errors"
	"siplatform/internal/blobrefs"
	"strings"
	"testing"
)

func TestSourceMetadataPublicationAndEpoch(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "source", "admin", "session")
	d := create(t, s, a, "SIQ name")
	_, e := p.Exec(ctx, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit,epoch) VALUES('sibrowser','source',100,10000000,2);`)
	if e != nil {
		t.Fatal(e)
	}
	// Receipt linkage is the same provenance check used by automatic publication.
	_, e = p.Exec(ctx, `INSERT INTO catalogue_imports(id,owner_id,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id,pack_id,origin,published_once) VALUES('receipt','source','sibrowser','123','hash',$2,'x.siq',1,'https://www.sibrowser.ru/packages/123','','Author','','',$1,'external_import',true)`, d.Card.ID, strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	m := SourceMetadata{Name: "Site name", Filename: "real.siq", Authors: []SourceLink{{"One", "https://www.sibrowser.ru/authors/One"}, {"Two", "https://www.sibrowser.ru/authors/Two"}}, Tags: []SourceLink{{"Science", "https://www.sibrowser.ru/tags/Science"}}, Description: []string{"Unique description"}}
	if e = s.SaveSourceMetadata(ctx, a, "123", 1, m); !errors.Is(e, ErrConflict) {
		t.Fatal("old epoch", e)
	}
	if e = s.SaveSourceMetadata(ctx, a, "123", 2, m); e != nil {
		t.Fatal(e)
	}

	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	private, err := s.Get(ctx, a, d.Card.ID, 1)
	if err != nil || private.Card.Name != "SIQ name" {
		t.Errorf("source snapshot rewrote private revision: %s %v", private.Card.Name, err)
	}
	c, e := s.Public(ctx, d.Card.ID)
	if e != nil || c.Name != "Site name" || c.Author != "One, Two" || len(c.Tags) != 1 {
		t.Fatal(c, e)
	}
	cards, e := s.Catalogue(ctx, Filter{Query: "Unique description", Tag: "Science"})
	if e != nil || len(cards) != 1 {
		t.Fatal(cards, e)
	}
	got, e := s.PublicSourceMetadata(ctx, d.Card.ID)
	if e != nil || got.Metadata.Name != m.Name || got.Document.Name != "SIQ name" {
		t.Fatal(got, e)
	}
	m.CoverURL = "https://www.sibrowser.ru/packages/123/logo.webp"
	if e = s.SaveSourceMetadata(ctx, a, "123", 2, m); e != nil {
		t.Fatal(e)
	}
	image := preparedImage()
	upload := strings.Repeat("b", 32)
	readyMedia(t, p, a, upload, image)
	reserved, err := s.Usage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, e = p.Exec(ctx, `UPDATE external_sources SET storage_limit=$1`, reserved.Storage.UsedBytes); e != nil {
		t.Fatal(e)
	}
	if e = s.AttachSourceCover(ctx, a, "123", 2, upload, m.CoverURL); e != nil {
		t.Fatal(e)
	}
	cover, e := s.PublicCover(ctx, d.Card.ID)
	if e != nil || cover.Key != image.Prepared.Standard.Key {
		t.Fatal(cover, e)
	}
	before, e := s.Usage(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AttachSourceCover(ctx, a, "123", 2, upload, m.CoverURL); e != nil {
		t.Fatal(e)
	}
	after, e := s.Usage(ctx, a)
	if e != nil || before.Storage.UsedBytes != after.Storage.UsedBytes {
		t.Fatal(before, after, e)
	}
	var revision int
	p.QueryRow(ctx, `SELECT current_revision FROM library_packs WHERE id=$1`, d.Card.ID).Scan(&revision)
	if revision != 1 {
		t.Fatal("changed game revision")
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=NULL WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PublicSourceMetadata(ctx, d.Card.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.PublicCover(ctx, d.Card.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cover after withdrawal", e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `DELETE FROM uploads WHERE id=$1`, upload); e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	refs, e := blobrefs.Check(ctx, tx, image.Prepared.Standard.Key)
	tx.Rollback(ctx)
	if e != nil || !refs.Content || refs.Ready {
		t.Fatal("lost cover after upload retention", refs, e)
	}
	retained, e := s.Usage(ctx, a)
	if e != nil || retained.Storage.UsedBytes != after.Storage.UsedBytes {
		t.Fatal("consumed upload changed quota on retention", retained, e)
	}
	m.CoverURL = ""
	if e = s.SaveSourceMetadata(ctx, a, "123", 2, m); e != nil {
		t.Fatal(e)
	}
	if e = s.AttachSourceCover(ctx, a, "123", 2, "", ""); e != nil {
		t.Fatal(e)
	}
	var queued bool
	if e = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM retention_candidates WHERE key=$1)`, image.Prepared.Standard.Key).Scan(&queued); e != nil || !queued {
		t.Fatal("old cover not enqueued", e)
	}
	if _, e = p.Exec(ctx, `UPDATE external_sources SET storage_limit=10000000`); e != nil {
		t.Fatal(e)
	}
	edited := document("Edited title")
	if _, e = s.UpdateDocument(ctx, a, d.Card.ID, 1, edited); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSourceMetadata(ctx, a, "123", 2, m); e != nil {
		t.Fatal("metadata stopped after editing", e)
	}
	history, e := s.History(ctx, a, d.Card.ID)
	if e != nil || len(history) != 2 || history[0].Name != "Edited title" || history[1].Name != "SIQ name" {
		t.Fatal("history rewritten", history, e)
	}

}
