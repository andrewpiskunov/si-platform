package library

import (
	"errors"
	"strings"
	"testing"
)

// Retained theme versions and unattached prepared uploads share the same quota
// gate as quiz revisions, even after ordinary theme deletion.
func TestThemeStorageCountsAgainstPackQuota(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	themeID := strings.Repeat("a", 32)
	if _, err := p.Exec(ctx, `INSERT INTO themes(id,owner_id,current_revision,deleted_at) VALUES($1,$2,1,now())`, themeID, a.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,1,'{}',$2)`, themeID, int64(10<<30-100)); err != nil {
		t.Fatal(err)
	}
	uploadID := strings.Repeat("b", 32)
	if _, err := p.Exec(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,purpose,result_theme) VALUES($1,$2,$3,'theme.zip',40,'ready','theme','{"size_bytes":100}')`, uploadID, a.User.ID, a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, a, Save{Document: document("No shared quota remaining")}); !errors.Is(err, ErrQuota) {
		t.Fatal("retained themes/ready derivatives escaped pack quota", err)
	}
	if _, err := p.Exec(ctx, `UPDATE theme_versions SET size_bytes=1 WHERE theme_id=$1`, themeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, a, Save{Document: document("Enough quota")}); err != nil {
		t.Fatal(err)
	}
}
