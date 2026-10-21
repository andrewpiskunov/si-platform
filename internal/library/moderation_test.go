package library

import (
	"errors"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/internal/accounts"
	"siplatform/migrations"
	"strings"
	"testing"
)

func TestDecisionHistoryPrivatePagination(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	d := create(t, s, owner, "History")
	publish(t, s, owner, mod, d, true)
	if _, err := s.Decide(ctx, mod, d.Card.ID, 1, "block", "Rights evidence"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []accounts.Identity{owner, mod, credential(t, p, "owner", "api", "drafts:read")} {
		page, err := s.DecisionHistory(ctx, a, d.Card.ID, 0, 1)
		if err != nil || len(page.Items) != 1 || page.NextAfterID == 0 {
			t.Fatal(page, err)
		}
		next, err := s.DecisionHistory(ctx, a, d.Card.ID, page.NextAfterID, 1)
		if err != nil || len(next.Items) != 1 || next.Items[0].Reason != "Rights evidence" || next.NextAfterID != 0 {
			t.Fatal(next, err)
		}
	}
	if _, err := s.DecisionHistory(ctx, other, d.Card.ID, 0, 50); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.DecisionHistory(ctx, credential(t, p, "owner", "api", "drafts:write"), d.Card.ID, 0, 50); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 51} {
		if _, err := s.DecisionHistory(ctx, owner, d.Card.ID, 0, limit); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestReportResolutionImmutableLiveAuthAndPages(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	d := create(t, s, owner, "Reports")
	publish(t, s, owner, mod, d, true)
	r, err := s.Report(ctx, other, d.Card.ID, 1, "Concern")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Report(ctx, owner, d.Card.ID, 1, "Other concern"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ReportPage(ctx, mod, "open", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextAfter == "" {
		t.Fatal(page, err)
	}
	next, err := s.ReportPage(ctx, mod, "open", page.NextAfter, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID <= page.Items[0].ID || next.NextAfter != "" {
		t.Fatal(next, err)
	}
	for _, reason := range []string{"", " \n", "bad\x01", strings.Repeat("я", 2001), string([]byte{255})} {
		if _, err = s.ResolveReport(ctx, mod, r.ID, "dismissed", reason); !errors.Is(err, ErrInvalid) {
			t.Fatal(reason, err)
		}
	}
	resolved, err := s.ResolveReport(ctx, mod, r.ID, "dismissed", "Checked\nrights")
	if err != nil || resolved.Resolution != "dismissed" || resolved.ResolvedBy == nil || *resolved.ResolvedBy != "mod" || resolved.ResolvedAt == nil {
		t.Fatal(resolved, err)
	}
	again, err := s.ResolveReport(ctx, mod, r.ID, "dismissed", "Checked\nrights")
	if err != nil || !again.ResolvedAt.Equal(*resolved.ResolvedAt) {
		t.Fatal(again, err)
	}
	if _, err = s.ResolveReport(ctx, mod, r.ID, "resolved", "Different"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ReportPage(ctx, other, "open", "", 50); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	token := credential(t, p, "mod", "api", "drafts:read")
	if _, err = s.ResolveReport(ctx, token, r.ID, "dismissed", "Checked\nrights"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, "UPDATE account_users SET role='author' WHERE id='mod'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveReport(ctx, mod, r.ID, "dismissed", "Checked\nrights"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", mod.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveReport(ctx, mod, r.ID, "dismissed", "Checked\nrights"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestHistoricBlockClosesAllOpenPreservesTerminal(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(map[bool]string{false: "republished", true: "withdrawn"}[withdraw], func(t *testing.T) {
			s, p := setup(t)
			owner := identity(t, p, "owner", "author", "session")
			mod := identity(t, p, "mod", "moderator", "session")
			other := identity(t, p, "other", "author", "session")
			d := create(t, s, owner, "Old")
			publish(t, s, owner, mod, d, true)
			old, err := s.Report(ctx, other, d.Card.ID, 1, "Old report")
			if err != nil {
				t.Fatal(err)
			}
			closed, err := s.Report(ctx, owner, d.Card.ID, 1, "Dismiss me")
			if err != nil {
				t.Fatal(err)
			}
			terminal, err := s.ResolveReport(ctx, mod, closed.ID, "dismissed", "Already checked")
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.UpdateDocument(ctx, owner, d.Card.ID, 1, document("New"))
			if err != nil {
				t.Fatal(err)
			}
			publish(t, s, owner, mod, second, true)
			newer, err := s.Report(ctx, other, d.Card.ID, 2, "New report")
			if err != nil {
				t.Fatal(err)
			}
			if withdraw {
				if err = s.Withdraw(ctx, owner, d.Card.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, decision := range []string{"approve", "reject"} {
				if _, err = s.Decide(ctx, mod, d.Card.ID, 1, decision, "stale"); !errors.Is(err, ErrConflict) {
					t.Fatal(decision, err)
				}
			}
			if _, err = s.Decide(ctx, mod, d.Card.ID, 1, "block", "Historic infringement"); err != nil {
				t.Fatal("historic block", err)
			}
			page, err := s.ReportPage(ctx, mod, "resolved", "", 50)
			if err != nil || len(page.Items) != 3 {
				t.Fatal(page, err)
			}
			for _, r := range page.Items {
				if r.ID == old.ID || r.ID == newer.ID {
					if r.Resolution != "decision" || r.ResolutionReason != "Historic infringement" || r.ResolvedBy == nil || r.ResolvedAt == nil {
						t.Fatal(r)
					}
				} else if r.Resolution != "dismissed" || r.ResolutionReason != terminal.ResolutionReason || !r.ResolvedAt.Equal(*terminal.ResolvedAt) {
					t.Fatal("terminal overwritten", r)
				}
			}
		})
	}
}

func TestResolveReportAfterWithdrawalOrDeletion(t *testing.T) {
	for _, state := range []string{"withdrawn", "pack_deleted", "owner_deleted"} {
		t.Run(state, func(t *testing.T) {
			s, p := setup(t)
			owner := identity(t, p, "owner", "author", "session")
			mod := identity(t, p, "mod", "moderator", "session")
			d := create(t, s, owner, "Reported")
			publish(t, s, owner, mod, d, true)
			r, err := s.Report(ctx, owner, d.Card.ID, 1, "Concern")
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "withdrawn":
				err = s.Withdraw(ctx, owner, d.Card.ID)
			case "pack_deleted":
				err = s.Delete(ctx, owner, d.Card.ID)
			case "owner_deleted":
				_, err = p.Exec(ctx, "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id='owner'")
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ResolveReport(ctx, mod, r.ID, "resolved", "Handled")
			if err != nil || got.Status != "resolved" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestConcurrentBlockAndResolutionKeepOneAudit(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	mod2 := identity(t, p, "mod2", "moderator", "session")
	d := create(t, s, owner, "Concurrent")
	publish(t, s, owner, mod, d, true)
	r, err := s.Report(ctx, owner, d.Card.ID, 1, "Concern")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := s.Decide(ctx, mod, d.Card.ID, 1, "block", "Block reason"); results <- err }()
	go func() {
		<-start
		_, err := s.ResolveReport(ctx, mod2, r.ID, "dismissed", "Dismiss reason")
		results <- err
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	page, err := s.ReportPage(ctx, mod, "resolved", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	got := page.Items[0]
	if got.ResolvedBy == nil || got.ResolvedAt == nil {
		t.Fatal(got)
	}
	if got.Resolution == "decision" {
		if got.ResolutionReason != "Block reason" || *got.ResolvedBy != "mod" {
			t.Fatal(got)
		}
	} else if got.Resolution != "dismissed" || got.ResolutionReason != "Dismiss reason" || *got.ResolvedBy != "mod2" {
		t.Fatal(got)
	}
}

func TestReportResolutionMigrationLegacyDownUp(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	d := create(t, s, owner, "Legacy")
	publish(t, s, owner, mod, d, true)
	r, err := s.Report(ctx, owner, d.Card.ID, 1, "Concern")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*p.Config().ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, "UPDATE library_reports SET status='resolved' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := s.ReportPage(ctx, mod, "resolved", "", 50)
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		got := page.Items[0]
		if got.Resolution != "legacy" || got.ResolvedBy != nil || got.ResolvedAt != nil || got.ResolutionReason != "" {
			t.Fatal("invented legacy audit", got)
		}
		if _, err = p.Exec(ctx, "UPDATE library_reports SET resolution='dismissed' WHERE id=$1", r.ID); err == nil {
			t.Fatal("missing terminal check")
		}
		if _, err = provider.DownTo(ctx, 12); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModerationPageValidationAndLiveReaders(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	d := create(t, s, owner, "Read")
	for _, tc := range []struct {
		status, after string
		limit         int
	}{{"all", "", 50}, {"open", "bad", 50}, {"open", strings.Repeat("A", 32), 50}, {"open", "", 0}, {"open", "", 51}} {
		if _, err := s.ReportPage(ctx, mod, tc.status, tc.after, tc.limit); !errors.Is(err, ErrInvalid) {
			t.Fatal(tc, err)
		}
	}
	if _, err := s.DecisionHistory(ctx, owner, d.Card.ID, -1, 50); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.ReportPage(ctx, credential(t, p, "mod", "api", "drafts:read"), "open", "", 50); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, "UPDATE account_users SET role='author' WHERE id='mod'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionHistory(ctx, mod, d.Card.ID, 0, 50); !errors.Is(err, ErrNotFound) {
		t.Fatal("demoted history", err)
	}
	if _, err := s.ReportPage(ctx, mod, "open", "", 50); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted list", err)
	}
	if _, err := p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionHistory(ctx, owner, d.Card.ID, 0, 50); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked history", err)
	}
}

func TestConcurrentReportResolutionsAreImmutable(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "mod", "moderator", "session")
	mod2 := identity(t, p, "mod2", "moderator", "session")
	d := create(t, s, owner, "Concurrent explicit")
	publish(t, s, owner, mod, d, true)
	r, err := s.Report(ctx, owner, d.Card.ID, 1, "Concern")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := s.ResolveReport(ctx, mod, r.ID, "resolved", "First finding")
		results <- err
	}()
	go func() {
		<-start
		_, err := s.ResolveReport(ctx, mod2, r.ID, "dismissed", "Second finding")
		results <- err
	}()
	close(start)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	page, err := s.ReportPage(ctx, mod, "resolved", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	got := page.Items[0]
	again, err := s.ResolveReport(ctx, mod2, r.ID, got.Resolution, got.ResolutionReason)
	if err != nil || again.ResolvedBy == nil || *again.ResolvedBy != *got.ResolvedBy || !again.ResolvedAt.Equal(*got.ResolvedAt) {
		t.Fatal("repeat changed first actor/time", again, err)
	}
}
