// Package accounts manages author identities and revocable credentials.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrInvalidInput  = errors.New("invalid_input")
	ErrUsernameTaken = errors.New("username_taken")
	ErrNotFound      = errors.New("not_found")
	ErrUnavailable   = errors.New("unavailable")
	ErrRateLimited   = errors.New("rate_limited")
)

const credentialTTL = 30 * 24 * time.Hour

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}
type Session struct {
	User      User      `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Identity struct {
	User      User
	Scopes    []string
	SessionID string
	APIToken  bool
}
type Options struct {
	Origin        string
	SecureCookies bool
}
type Service struct {
	pool        *pgxpool.Pool
	options     Options
	limitMu     sync.Mutex
	limits      map[string]*rateBucket
	globalLimit rateBucket
}

func New(pool *pgxpool.Pool, options Options) *Service {
	return &Service{pool: pool, options: options, limits: make(map[string]*rateBucket)}
}
func normalizeUsername(username string) (string, bool) {
	if len(username) < 3 || len(username) > 32 {
		return "", false
	}
	for _, c := range username {
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return "", false
		}
	}
	return strings.ToLower(username), true
}
func validLabel(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= max
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b[:]), nil
}
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(b) == 32
}
func safeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}
func (s *Service) Register(ctx context.Context, username, password, displayName string) (Session, error) {
	username, valid := normalizeUsername(username)
	if !valid || !validPassword(password) || !validLabel(displayName, 64) {
		return Session{}, ErrInvalidInput
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return Session{}, err
	}
	id, err := randomID()
	if err != nil {
		return Session{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	user := User{ID: id, Username: username, DisplayName: displayName, Role: "author"}
	err = tx.QueryRow(ctx, "INSERT INTO account_users (id,username,display_name,password_hash) VALUES ($1,$2,$3,$4) RETURNING created_at", id, username, displayName, hash).Scan(&user.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "account_users_username_key" {
			return Session{}, ErrUsernameTaken
		}
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
func (s *Service) Login(ctx context.Context, username, password string) (Session, error) {
	username, valid := normalizeUsername(username)
	if !validPassword(password) {
		return Session{}, ErrUnauthorized
	}
	var id, hash string
	err := s.pool.QueryRow(ctx, "SELECT id,password_hash FROM account_users WHERE username=$1 AND deleted_at IS NULL", username).Scan(&id, &hash)
	exists := err == nil && valid
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, safeError(ctx, err)
	}
	if !exists {
		hash = dummyPasswordHash
	}
	matches, err := checkPassword(ctx, password, hash)
	if err != nil {
		return Session{}, err
	}
	if !matches || !exists {
		return Session{}, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	user, currentHash, err := lockUser(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	// Rechecking after acquiring the user lock closes the hash-check/revocation race.
	if hash != currentHash {
		return Session{}, ErrUnauthorized
	}
	session, err := issueSession(ctx, tx, user)
	if err != nil {
		return Session{}, err
	}
	if err = audit(ctx, tx, id, "login", ""); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, safeError(ctx, err)
	}
	return session, nil
}
func issueSession(ctx context.Context, tx pgx.Tx, user User) (Session, error) {
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	id, err := randomID()
	if err != nil {
		return Session{}, err
	}
	session := Session{User: user, Token: token}
	err = tx.QueryRow(ctx, "INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES ($1,$2,$3,'session',clock_timestamp()+interval '30 days') RETURNING expires_at", id, user.ID, tokenHash(token)).Scan(&session.ExpiresAt)
	return session, safeError(ctx, err)
}
func audit(ctx context.Context, tx pgx.Tx, userID, action, targetID string) error {
	_, err := tx.Exec(ctx, "INSERT INTO account_audit(user_id,action,target_id) VALUES ($1,$2,$3)", userID, action, targetID)
	return safeError(ctx, err)
}
func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if !validToken(token) {
		return Identity{}, ErrUnauthorized
	}
	var identity Identity
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.role,u.created_at,c.id,c.scopes,c.kind
 FROM account_credentials c JOIN account_users u ON u.id=c.user_id
 WHERE c.token_hash=$1 AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND u.deleted_at IS NULL`, tokenHash(token)).Scan(&identity.User.ID, &identity.User.Username, &identity.User.DisplayName, &identity.User.Role, &identity.User.CreatedAt, &identity.SessionID, &identity.Scopes, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrUnauthorized
	}
	if err != nil {
		return Identity{}, safeError(ctx, err)
	}
	identity.APIToken = kind == "api"
	return identity, nil
}
func requestToken(r *http.Request) (string, error) {
	if values, present := r.Header["Authorization"]; present {
		if len(values) != 1 {
			return "", ErrUnauthorized
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return "", ErrUnauthorized
		}
		return parts[1], nil
	}
	cookie, err := r.Cookie("si_session")
	if err != nil {
		return "", ErrUnauthorized
	}
	return cookie.Value, nil
}
func (s *Service) FromRequest(r *http.Request) (Identity, error) {
	token, err := requestToken(r)
	if err != nil {
		return Identity{}, err
	}
	return s.Authenticate(r.Context(), token)
}
func lockUser(ctx context.Context, tx pgx.Tx, id string) (User, string, error) {
	var user User
	var hash string
	err := tx.QueryRow(ctx, "SELECT id,username,display_name,role,created_at,password_hash FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrUnauthorized
	}
	return user, hash, safeError(ctx, err)
}

// lockSession always locks the user before rechecking the credential. Every
// credential writer uses that same order, so revocation cannot be bypassed by
// a request that authenticated before waiting for an account mutation.
func (s *Service) lockSession(ctx context.Context, tx pgx.Tx, token string) (Identity, string, error) {
	if !validToken(token) {
		return Identity{}, "", ErrUnauthorized
	}
	var userID string
	err := tx.QueryRow(ctx, "SELECT user_id FROM account_credentials WHERE token_hash=$1", tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, "", ErrUnauthorized
	}
	if err != nil {
		return Identity{}, "", safeError(ctx, err)
	}
	user, hash, err := lockUser(ctx, tx, userID)
	if err != nil {
		return Identity{}, "", err
	}
	identity := Identity{User: user}
	var kind string
	err = tx.QueryRow(ctx, "SELECT id,kind FROM account_credentials WHERE user_id=$1 AND token_hash=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()", userID, tokenHash(token)).Scan(&identity.SessionID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, "", ErrUnauthorized
	}
	if err != nil {
		return Identity{}, "", safeError(ctx, err)
	}
	if kind != "session" {
		return Identity{}, "", ErrForbidden
	}
	return identity, hash, nil
}
func (s *Service) logout(ctx context.Context, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	identity, _, err := s.lockSession(ctx, tx, token)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=clock_timestamp() WHERE id=$1", identity.SessionID); err != nil {
		return safeError(ctx, err)
	}
	if err = audit(ctx, tx, identity.User.ID, "logout", identity.SessionID); err != nil {
		return err
	}
	return safeError(ctx, tx.Commit(ctx))
}
func (s *Service) passwordHashForSession(ctx context.Context, token string) (string, error) {
	if !validToken(token) {
		return "", ErrUnauthorized
	}
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT u.password_hash FROM account_users u JOIN account_credentials c ON c.user_id=u.id
 WHERE c.token_hash=$1 AND c.kind='session' AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND u.deleted_at IS NULL`, tokenHash(token)).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthorized
	}
	return hash, safeError(ctx, err)
}
func (s *Service) changePassword(ctx context.Context, token, currentPassword, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrInvalidInput
	}
	return s.changeIdentity(ctx, token, currentPassword, newPassword, false)
}
func (s *Service) deleteAccount(ctx context.Context, token, password string) error {
	return s.changeIdentity(ctx, token, password, "", true)
}
func (s *Service) changeIdentity(ctx context.Context, token, currentPassword, newPassword string, deleting bool) error {
	if !validPassword(currentPassword) {
		return ErrUnauthorized
	}
	originalHash, err := s.passwordHashForSession(ctx, token)
	if err != nil {
		return err
	}
	matches, err := checkPassword(ctx, currentPassword, originalHash)
	if err != nil {
		return err
	}
	if !matches {
		return ErrUnauthorized
	}
	var replacementHash string
	if !deleting {
		replacementHash, err = hashPassword(ctx, newPassword)
		if err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	identity, currentHash, err := s.lockSession(ctx, tx, token)
	if err != nil {
		return err
	}
	if currentHash != originalHash {
		return ErrUnauthorized
	}
	action := "password_changed"
	if deleting {
		action = "deleted"
		_, err = tx.Exec(ctx, "UPDATE account_users SET username=NULL,display_name='Deleted user',password_hash=NULL,role='author',deleted_at=clock_timestamp() WHERE id=$1", identity.User.ID)
	} else {
		_, err = tx.Exec(ctx, "UPDATE account_users SET password_hash=$2 WHERE id=$1", identity.User.ID, replacementHash)
	}
	if err != nil {
		return safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", identity.User.ID); err != nil {
		return safeError(ctx, err)
	}
	if deleting {
		if _, err = tx.Exec(ctx, "UPDATE account_credentials SET name='' WHERE user_id=$1", identity.User.ID); err != nil {
			return safeError(ctx, err)
		}
	}
	if err = audit(ctx, tx, identity.User.ID, action, ""); err != nil {
		return err
	}
	return safeError(ctx, tx.Commit(ctx))
}

type tokenMetadata struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}
type issuedToken struct {
	tokenMetadata
	Token string `json:"token"`
}

func validScopes(scopes []string) bool {
	if len(scopes) == 0 || len(scopes) > 4 {
		return false
	}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		switch scope {
		case "drafts:read", "drafts:write", "drafts:export", "ai:generate":
		default:
			return false
		}
		if seen[scope] {
			return false
		}
		seen[scope] = true
	}
	return true
}
func (s *Service) createToken(ctx context.Context, sessionToken, name string, scopes []string) (issuedToken, error) {
	if !validLabel(name, 64) || !validScopes(scopes) {
		return issuedToken{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return issuedToken{}, safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	identity, _, err := s.lockSession(ctx, tx, sessionToken)
	if err != nil {
		return issuedToken{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM account_credentials WHERE user_id=$1 AND kind='api' AND revoked_at IS NULL AND expires_at>clock_timestamp()", identity.User.ID).Scan(&count); err != nil {
		return issuedToken{}, safeError(ctx, err)
	}
	if count >= 100 {
		return issuedToken{}, ErrRateLimited
	}
	id, err := randomID()
	if err != nil {
		return issuedToken{}, err
	}
	token, err := randomToken()
	if err != nil {
		return issuedToken{}, err
	}
	out := issuedToken{tokenMetadata: tokenMetadata{ID: id, Name: name, Scopes: scopes}, Token: token}
	err = tx.QueryRow(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,name,scopes,expires_at)
 VALUES($1,$2,$3,'api',$4,$5,clock_timestamp()+interval '30 days') RETURNING created_at,expires_at`, id, identity.User.ID, tokenHash(token), name, scopes).Scan(&out.CreatedAt, &out.ExpiresAt)
	if err != nil {
		return issuedToken{}, safeError(ctx, err)
	}
	if err = audit(ctx, tx, identity.User.ID, "token_created", id); err != nil {
		return issuedToken{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return issuedToken{}, safeError(ctx, err)
	}
	return out, nil
}
func (s *Service) revokeToken(ctx context.Context, sessionToken, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return safeError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	identity, _, err := s.lockSession(ctx, tx, sessionToken)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1 AND user_id=$2 AND kind='api'", id, identity.User.ID)
	if err != nil {
		return safeError(ctx, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, identity.User.ID, "token_revoked", id); err != nil {
		return err
	}
	return safeError(ctx, tx.Commit(ctx))
}
func (s *Service) listTokens(ctx context.Context, userID string) ([]tokenMetadata, error) {
	rows, err := s.pool.Query(ctx, "SELECT id,name,scopes,created_at,expires_at,revoked_at FROM account_credentials WHERE user_id=$1 AND kind='api' ORDER BY created_at,id", userID)
	if err != nil {
		return nil, safeError(ctx, err)
	}
	defer rows.Close()
	out := make([]tokenMetadata, 0)
	for rows.Next() {
		var token tokenMetadata
		if err := rows.Scan(&token.ID, &token.Name, &token.Scopes, &token.CreatedAt, &token.ExpiresAt, &token.RevokedAt); err != nil {
			return nil, safeError(ctx, err)
		}
		out = append(out, token)
	}
	return out, safeError(ctx, rows.Err())
}
