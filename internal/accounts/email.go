package accounts

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
)

func normalizeEmail(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(value, "@") {
		return "", false
	}
	return value, true
}
func validDisplayName(name string) bool {
	if !validLabel(name, 64) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (s *Service) RegisterEmail(ctx context.Context, email, password, name string) error {
	email, ok := normalizeEmail(email)
	name = strings.TrimSpace(name)
	if !ok || !validPassword(password) || !validDisplayName(name) {
		return ErrInvalidInput
	}
	if s.options.SendMail == nil {
		return ErrUnavailable
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return err
	}
	var exists bool
	if err = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM account_users WHERE email=$1)", email).Scan(&exists); err != nil {
		return safeError(ctx, err)
	}
	if exists {
		return nil
	}
	return s.sendEmailRequest(ctx, "verify", email, "", hash, name)
}
func (s *Service) RequestReset(ctx context.Context, email string) error {
	email, ok := normalizeEmail(email)
	if !ok {
		return ErrInvalidInput
	}
	if s.options.SendMail == nil {
		return ErrUnavailable
	}
	var id string
	err := s.pool.QueryRow(ctx, "SELECT id FROM account_users WHERE email=$1 AND deleted_at IS NULL", email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return safeError(ctx, err)
	}
	return s.sendEmailRequest(ctx, "reset", email, id, "", "")
}
func (s *Service) sendEmailRequest(ctx context.Context, purpose, email, id, hash, name string) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	// Serialize per-address issuance and throttle mail independently of IP limits.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", email); err != nil {
		return safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM account_email_requests WHERE expires_at<clock_timestamp()"); err != nil {
		return safeError(ctx, err)
	}
	var recent bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM account_email_requests WHERE email=$1 AND purpose=$2 AND created_at>clock_timestamp()-interval '1 minute')", email, purpose).Scan(&recent); err != nil {
		return safeError(ctx, err)
	}
	if recent {
		return nil
	}
	ttl := "30 minutes"
	subject := "Восстановление пароля — Своя игра"
	if purpose == "verify" {
		ttl = "24 hours"
		subject = "Подтверждение почты — Своя игра"
	}
	_, err = tx.Exec(ctx, `INSERT INTO account_email_requests(token_hash,purpose,email,user_id,password_hash,display_name,expires_at) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),clock_timestamp()+$7::interval)`, tokenHash(token), purpose, email, id, hash, name, ttl)
	if err != nil {
		return safeError(ctx, err)
	}
	origin := strings.TrimRight(s.options.Origin, "/")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrUnavailable
	}
	body := subject + "\n\nОткройте ссылку и подтвердите действие:\n" + origin + "/login?mode=" + purpose + "#token=" + token + "\n\nЕсли вы не запрашивали это письмо, проигнорируйте его."
	if err = s.options.SendMail(ctx, email, subject, body); err != nil {
		return ErrUnavailable
	}
	return safeError(ctx, tx.Commit(ctx))
}
func (s *Service) VerifyEmail(ctx context.Context, token string) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	var email, hash, name string
	// Atomic consumption also serializes attempts to reuse a link.
	err = tx.QueryRow(ctx, `DELETE FROM account_email_requests WHERE token_hash=$1 AND purpose='verify' AND expires_at>clock_timestamp() RETURNING email,password_hash,display_name`, tokenHash(token)).Scan(&email, &hash, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidInput
	}
	if err != nil {
		return Session{}, safeError(ctx, err)
	}
	id, err := randomID()
	if err != nil {
		return Session{}, err
	}
	user := User{ID: id, Username: id, DisplayName: name, Role: "author"}
	err = tx.QueryRow(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,email) VALUES($1,$1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING created_at`, id, name, hash, email).Scan(&user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidInput
	}
	if err != nil {
		return Session{}, safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM account_email_requests WHERE email=$1 AND purpose='verify'", email); err != nil {
		return Session{}, safeError(ctx, err)
	}
	session, err := issueSession(ctx, tx, user)
	if err != nil {
		return Session{}, err
	}
	if err = audit(ctx, tx, id, "registered", ""); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, safeError(ctx, err)
	}
	return session, nil
}
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if !validToken(token) || !validPassword(password) {
		return ErrInvalidInput
	}
	var id string
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM account_email_requests WHERE token_hash=$1 AND purpose='reset' AND expires_at>clock_timestamp()`, tokenHash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidInput
	}
	if err != nil {
		return safeError(ctx, err)
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	if _, _, err = lockUser(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM account_email_requests WHERE token_hash=$1 AND user_id=$2 AND purpose='reset' AND expires_at>clock_timestamp()`, tokenHash(token), id)
	if err != nil {
		return safeError(ctx, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalidInput
	}
	if _, err = tx.Exec(ctx, "UPDATE account_users SET password_hash=$2 WHERE id=$1", id, hash); err != nil {
		return safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", id); err != nil {
		return safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM account_email_requests WHERE user_id=$1", id); err != nil {
		return safeError(ctx, err)
	}
	if err = audit(ctx, tx, id, "password_changed", ""); err != nil {
		return err
	}
	return safeError(ctx, tx.Commit(ctx))
}
