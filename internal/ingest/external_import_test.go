package ingest

import (
	"bytes"
	"errors"
	"siplatform/internal/library"
	"testing"
)

// Catches accidentally treating source imports as private drafts or allowing
// a replay to undo an operator withdrawal.
func TestExternalImportPublishesOnceAndRespectsWithdrawal(t *testing.T) {
	f := setup(t)
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit) VALUES('sibrowser',$1,100000,1099511627776)`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	data := archive(t, textXML, nil)
	c := catalogueInput(data)
	in := ExternalInput{ExternalID: "123", Name: "123.siq", Size: int64(len(data)), SHA256: c.SHA256, Attribution: "Source author"}
	u, e := f.service.CreateExternal(bg, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Complete(bg, f.owner, u.ID); e != nil {
		t.Fatal(e)
	}
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	for range 2 {
		card, e := f.lib.PublishExternal(bg, f.owner, "sibrowser", "123")
		if e != nil {
			t.Fatal(e)
		}
		if card.ID != u.DraftID || card.Status != "published" || card.Rights.Confirmed || card.AllowDownload || card.Rights.SourceURL != "https://www.sibrowser.ru/packages/123" || card.Rights.Origin != "external_import" {
			t.Fatalf("bad public card: %+v", card)
		}
	}
	if _, e = f.lib.ForPlay(bg, f.owner, u.DraftID, 0); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = f.pool.QueryRow(bg, `SELECT count(*) FROM library_decisions WHERE pack_id=$1`, u.DraftID).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e = f.lib.Withdraw(bg, f.owner, u.DraftID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.lib.PublishExternal(bg, f.owner, "sibrowser", "123"); !errors.Is(e, library.ErrConflict) {
		t.Fatal("resurrected", e)
	}
	if _, e = f.lib.Public(bg, u.DraftID); !errors.Is(e, library.ErrNotFound) {
		t.Fatal(e)
	}
	in.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, e = f.service.CreateExternal(bg, f.owner, in); !errors.Is(e, ErrConflict) {
		t.Fatal("changed bytes", e)
	}
}

func TestExternalImportRequiresConfiguredLiveAdmin(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	c := catalogueInput(data)
	in := ExternalInput{ExternalID: "123", Name: "123.siq", Size: int64(len(data)), SHA256: c.SHA256, Attribution: "Author"}
	if _, e := f.service.CreateExternal(bg, f.owner, in); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit) VALUES('sibrowser',$1,1000,107374182400)`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.CreateExternal(bg, f.owner, in); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.CreateExternal(bg, f.owner, in); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `UPDATE account_credentials SET revoked_at=now() WHERE id=$1`, f.owner.SessionID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.CreateExternal(bg, f.owner, in); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
}

func TestExternalRetryKeepsReceiptAndRechecksBytes(t *testing.T) {
	f := setup(t)
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit) VALUES('sibrowser',$1,1000,1099511627776)`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	data := archive(t, textXML, nil)
	c := catalogueInput(data)
	in := ExternalInput{ExternalID: "123", Name: "123.siq", Size: int64(len(data)), SHA256: c.SHA256, Attribution: "Author"}
	u, e := f.service.CreateExternal(bg, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(bg, `UPDATE uploads SET status='failed',error='expired' WHERE id=$1`, u.ID); e != nil {
		t.Fatal(e)
	}
	next, e := f.service.RetryExternal(bg, f.owner, in)
	if e != nil || next.ID != u.ID || next.Status != "uploading" || next.Offset != 0 {
		t.Fatal(next, e)
	}
	if _, e = f.service.Append(bg, f.owner, next.ID, 0, bytes.NewReader(data), int64(len(data))); e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Complete(bg, f.owner, next.ID); e != nil {
		t.Fatal(e)
	}
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	next, e = f.service.RetryExternal(bg, f.owner, in)
	if e != nil || next.DraftID != u.DraftID {
		t.Fatal(next, e)
	}
}
