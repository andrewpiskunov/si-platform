package library

import (
	"errors"
	"strings"
	"testing"
)

func TestUsageMatchesChargedStorageAndOwnLimits(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "quota_owner", "author", "session")
	other := identity(t, p, "quota_other", "author", "session")
	d := create(t, s, owner, "Own")
	foreign := create(t, s, other, "Other")
	if _, e := s.Update(ctx, owner, d.Card.ID, 1, Save{Document: document("Second")}); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, "UPDATE library_revisions SET size_bytes=revision*100 WHERE pack_id=$1", d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, "UPDATE library_revisions SET size_bytes=9999 WHERE pack_id=$1", foreign.Card.ID); e != nil {
		t.Fatal(e)
	}
	theme := strings.Repeat("a", 32)
	if _, e := p.Exec(ctx, "INSERT INTO themes(id,owner_id,current_revision,deleted_at) VALUES($1,$2,1,now())", theme, owner.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, "INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,1,'{}',40)", theme); e != nil {
		t.Fatal(e)
	}
	for i, status := range []string{"ready", "uploading", "queued", "processing"} {
		id := strings.Repeat(string(rune('b'+i)), 32)
		expires := "1 day"
		if status == "processing" {
			expires = "-1 day"
		}
		asset := `null`
		if status == "ready" {
			asset = `{"size":20,"prepared":{"standard":{"size":10},"economy":{"size":5}}}`
		}
		if _, e := p.Exec(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,purpose,media_kind,result_asset,expires_at) VALUES($1,$2,$3,'fixture.png',20,$4,'media','image',NULLIF($5::jsonb,'null'::jsonb),clock_timestamp()+$6::interval)`, id, owner.User.ID, owner.SessionID, status, asset, expires); e != nil {
			t.Fatal(e)
		}
	}
	usage, e := s.Usage(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	if usage.Storage.UsedBytes != 375 || usage.Storage.LimitBytes != 10<<30 || usage.Storage.RemainingBytes != (10<<30)-375 {
		t.Fatalf("storage %+v", usage.Storage)
	}
	if usage.Packs.Used != 1 || usage.Packs.Limit != 200 || usage.Packs.Remaining != 199 {
		t.Fatalf("packs %+v", usage.Packs)
	}
	if usage.Themes.Used != 1 || usage.Themes.Limit != 100 || usage.Themes.Remaining != 99 {
		t.Fatalf("retained theme %+v", usage.Themes)
	}
	if usage.Uploads.Active != 2 || usage.Uploads.Limit != 2 || usage.Uploads.Remaining != 0 {
		t.Fatalf("uploads %+v", usage.Uploads)
	}
	if usage.Limits.PackRevisions != 500 || usage.Limits.ThemeRevisions != 100 || usage.Limits.SIQBytes != 250<<20 || usage.Limits.MediaBytes != 100<<20 || usage.Limits.ThemeBytes != 5<<20 {
		t.Fatalf("limits %+v", usage.Limits)
	}
	var status string
	if e = p.QueryRow(ctx, "SELECT status FROM uploads WHERE id=$1", strings.Repeat("e", 32)).Scan(&status); e != nil || status != "processing" {
		t.Fatal("GET mutated expired upload", status, e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	charged, e := StorageUsage(ctx, tx, owner.User.ID)
	_ = tx.Rollback(ctx)
	if e != nil || charged != usage.Storage.UsedBytes {
		t.Fatal("usage diverges", charged, e)
	}
	if _, e = p.Exec(ctx, "UPDATE library_revisions SET size_bytes=$2 WHERE pack_id=$1 AND revision=1", d.Card.ID, int64(11<<30)); e != nil {
		t.Fatal(e)
	}
	usage, e = s.Usage(ctx, owner)
	if e != nil || usage.Storage.RemainingBytes != 0 {
		t.Fatal("negative capacity", usage, e)
	}
	// Deleting a pack does not drop an unattached ready media charge or retained theme.
	if _, e = p.Exec(ctx, "INSERT INTO library_media_receipts(pack_id,upload_id,revision) VALUES($1,$2,2)", d.Card.ID, strings.Repeat("b", 32)); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, owner, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	usage, e = s.Usage(ctx, owner)
	if e != nil || usage.Packs.Used != 0 || usage.Storage.UsedBytes != 75 || usage.Themes.Used != 1 {
		t.Fatal("retained accounting after deletion", usage, e)
	}

}
func TestUsageRequiresCurrentAccountSession(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "quota_author", "author", "session")
	token := credential(t, p, "quota_author", "api", "drafts:read", "drafts:write")
	if _, e := s.Usage(ctx, token); !errors.Is(e, ErrForbidden) {
		t.Fatal("token usage access", e)
	}
	if _, e := p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", a.SessionID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Usage(ctx, a); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("revoked usage access", e)
	}
	b := identity(t, p, "quota_deleted", "author", "session")
	if _, e := p.Exec(ctx, "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1", b.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Usage(ctx, b); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("deleted usage access", e)
	}
}
