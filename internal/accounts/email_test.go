package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEmailLifecycle(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	var link string
	s.options.SendMail = func(_ context.Context, to, subject, body string) error { link = body; return nil }
	if err := s.RegisterEmail(ctx, " Test@Example.com ", testPassword, "Аня · 日本語 · العربية"); err != nil {
		t.Fatal(err)
	}
	token := emailTokenFromBody(link)
	if _, err := s.Login(ctx, "test@example.com", testPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("unverified login", err)
	}
	session, err := s.VerifyEmail(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if session.User.DisplayName != "Аня · 日本語 · العربية" {
		t.Fatal(session.User.DisplayName)
	}
	if _, err = s.VerifyEmail(ctx, token); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("replay", err)
	}
	if _, err = s.Login(ctx, "TEST@example.com", testPassword); err != nil {
		t.Fatal(err)
	}
	if err = s.RequestReset(ctx, "test@example.com"); err != nil {
		t.Fatal(err)
	}
	reset := emailTokenFromBody(link)
	if err = s.ResetPassword(ctx, reset, "new long password 123"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old session survived", err)
	}
	if err = s.ResetPassword(ctx, reset, testPassword); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("reset replay", err)
	}
	if _, err = s.Login(ctx, "test@example.com", "new long password 123"); err != nil {
		t.Fatal(err)
	}
	link = ""
	if err = s.RequestReset(ctx, "unknown@example.com"); err != nil || link != "" {
		t.Fatal("unknown address", err)
	}
	if err = s.RegisterEmail(ctx, "test@example.com", testPassword, "Other"); err != nil || link != "" {
		t.Fatal("duplicate address", err)
	}
	if err = s.RequestReset(ctx, "test@example.com"); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "UPDATE account_email_requests SET expires_at=clock_timestamp()-interval '1 second'")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ResetPassword(ctx, emailTokenFromBody(link), testPassword); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("expired", err)
	}
	register(t, s, "legacy_user")
	if _, err = s.Login(ctx, "legacy_user", testPassword); err != nil {
		t.Fatal(err)
	}
}

func emailTokenFromBody(body string) string {
	_, tail, _ := strings.Cut(body, "#token=")
	return strings.Fields(tail)[0]
}

func TestEmailExpiryDeliveryFailureAndPrivacy(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	var body string
	s.options.SendMail = func(_ context.Context, _, _, text string) error { body = text; return errors.New("delivery failed") }
	if err := s.RegisterEmail(ctx, "retry@example.com", testPassword, "李 明"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM account_email_requests").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed delivery persisted", count, err)
	}
	s.options.SendMail = func(_ context.Context, _, _, text string) error { body = text; return nil }
	if err := s.RegisterEmail(ctx, "retry@example.com", testPassword, "李 明"); err != nil {
		t.Fatal(err)
	}
	token := emailTokenFromBody(body)
	var stored string
	if err := pool.QueryRow(ctx, "SELECT token_hash FROM account_email_requests").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != tokenHash(token) {
		t.Fatal("raw token stored")
	}
	body = ""
	if err := s.RegisterEmail(ctx, "retry@example.com", testPassword, "李 明"); err != nil || body != "" {
		t.Fatal("resend throttle", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE account_email_requests SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyEmail(ctx, token); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("expired verify accepted", err)
	}
	for _, name := range []string{"Александр", "山田 太郎", "ليلى", "Élodie", "अनया"} {
		if !validDisplayName(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{" ", "a\nb"} {
		if validDisplayName(name) {
			t.Fatal("invalid name accepted")
		}
	}
	for _, address := range []string{"not-an-email", "Name <name@example.com>", "a@example.com\r\nBcc: x@example.com"} {
		if _, ok := normalizeEmail(address); ok {
			t.Fatal("bad email accepted")
		}
	}
}
