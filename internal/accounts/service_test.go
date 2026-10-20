package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

const testPassword = "correct horse battery staple"

func setup(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return New(pool, Options{Origin: "https://quiz.example", SecureCookies: true}), pool
}
func register(t *testing.T, s *Service, username string) Session {
	t.Helper()
	session, err := s.Register(context.Background(), username, testPassword, "Автор")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// Catches plaintext credentials, missing normalization/role defaults, and broken persisted authentication.
func TestRegistrationLoginAndHashedStorage(t *testing.T) {
	s, pool := setup(t)
	session := register(t, s, "AuThOr_1")
	if session.User.Username != "author_1" || session.User.DisplayName != "Автор" || session.User.Role != "author" || len(session.User.ID) != 32 {
		t.Fatal("invalid user identity")
	}
	if ttl := time.Until(session.ExpiresAt); ttl < 29*24*time.Hour || ttl > 30*24*time.Hour {
		t.Fatalf("invalid session lifetime: %s", ttl)
	}
	var hash, tokenHash string
	if err := pool.QueryRow(context.Background(), "SELECT u.password_hash, c.token_hash FROM account_users u JOIN account_credentials c ON c.user_id=u.id WHERE u.id=$1", session.User.ID).Scan(&hash, &tokenHash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") || strings.Contains(hash, testPassword) {
		t.Fatal("password not safely hashed")
	}
	digest := sha256.Sum256([]byte(session.Token))
	if tokenHash != hex.EncodeToString(digest[:]) || tokenHash == session.Token {
		t.Fatal("token not safely hashed")
	}
	login, err := New(pool, Options{}).Login(context.Background(), "AUTHOR_1", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if login.User.ID != session.User.ID || login.Token == session.Token {
		t.Fatal("login did not issue independent session")
	}
	identity, err := s.Authenticate(context.Background(), login.Token)
	if err != nil || identity.User.ID != session.User.ID || identity.APIToken || identity.SessionID == "" {
		t.Fatal("session authentication failed")
	}
	wrong, err := s.Login(context.Background(), "author_1", "an incorrect password")
	if !errors.Is(err, ErrUnauthorized) || wrong.Token != "" {
		t.Fatal("wrong password accepted")
	}
	_, missingErr := s.Login(context.Background(), "missing", testPassword)
	if !errors.Is(missingErr, ErrUnauthorized) || err.Error() != missingErr.Error() {
		t.Fatal("login leaks account existence")
	}
}

// Catches database uniqueness races and reuse of password salts.
func TestConcurrentUsernameCollision(t *testing.T) {
	s, pool := setup(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"duplicate", "DUPLICATE"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			_, err := s.Register(context.Background(), name, testPassword, "User")
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrUsernameTaken) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	second := register(t, s, "second")
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(DISTINCT password_hash) FROM account_users").Scan(&count); err != nil || count != 2 {
		t.Fatal("identical passwords reused a hash", err)
	}
	if _, err := s.Authenticate(context.Background(), second.Token+"x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("modified token accepted")
	}
}

// Catches expiry checks missing on persisted sessions and unsafe Bearer-to-cookie fallback.
func TestSessionExpiryAndRequestPrecedence(t *testing.T) {
	s, pool := setup(t)
	session := register(t, s, "expiry")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", "si_session="+session.Token)
	if _, err := s.FromRequest(req); err != nil {
		t.Fatal(err)
	}
	for _, authorization := range []string{"Bearer invalid", "Basic invalid", "Bearer", "Bearer "} {
		req.Header.Set("Authorization", authorization)
		if _, err := s.FromRequest(req); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("invalid authorization fell back to cookie")
		}
	}
	req.Header.Set("Authorization", "Bearer "+session.Token)
	if _, err := s.FromRequest(req); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE account_credentials SET expires_at=now()-interval '1 second' WHERE user_id=$1", session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
}

// Catches accepting invalid identity data before expensive password hashing.
func TestRegistrationValidation(t *testing.T) {
	s, _ := setup(t)
	cases := []struct{ username, password, displayName string }{
		{"ab", testPassword, "User"}, {strings.Repeat("a", 33), testPassword, "User"}, {"имя", testPassword, "User"},
		{"user-name", testPassword, "User"}, {" valid", testPassword, "User"}, {"valid", "short", "User"},
		{"valid", strings.Repeat("a", 129), "User"}, {"valid", testPassword, ""}, {"valid", testPassword, strings.Repeat("Я", 65)},
	}
	for _, c := range cases {
		if _, err := s.Register(context.Background(), c.username, c.password, c.displayName); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid registration accepted")
		}
	}
	session, err := s.Register(context.Background(), "unicode", strings.Repeat("Я", 12), strings.Repeat("Я", 64))
	if err != nil || session.User.DisplayName != strings.Repeat("Я", 64) {
		t.Fatal("Unicode character bounds rejected", err)
	}
}

// Catches accepting a cancelled request into the bounded Argon2 worker queue.
func TestPasswordWorkCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hashPassword(ctx, testPassword); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled password work ran", err)
	}
}

// Catches issuing a session after another transaction has changed the password
// between Login's expensive hash check and its acquisition of the user lock.
func TestLoginRechecksPasswordAfterUserLock(t *testing.T) {
	s, pool := setup(t)
	owner := register(t, s, "login_race")
	replacement, err := hashPassword(context.Background(), "replacement long password")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT id FROM account_users WHERE id=$1 FOR UPDATE", owner.User.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Login(ctx, owner.User.Username, testPassword); result <- err }()
	waitForUserLock(t, pool, tx.Conn().PgConn().PID())
	if _, err := tx.Exec(ctx, "UPDATE account_users SET password_hash=$2 WHERE id=$1", owner.User.ID, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=clock_timestamp() WHERE user_id=$1", owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old-password login survived concurrent password replacement", err)
	}
}

// Catches API token creation that trusts a session authenticated before waiting
// on a concurrent logout, password change, or account deletion transaction.
func TestTokenCreationRechecksRevokedSessionAfterUserLock(t *testing.T) {
	s, pool := setup(t)
	owner := register(t, s, "token_race")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT id FROM account_users WHERE id=$1 FOR UPDATE", owner.User.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.createToken(ctx, owner.Token, "Racing token", []string{"drafts:read"})
		result <- err
	}()
	waitForUserLock(t, pool, tx.Conn().PgConn().PID())
	if _, err := tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=clock_timestamp() WHERE user_id=$1", owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked session minted API token", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM account_credentials WHERE user_id=$1 AND kind='api'", owner.User.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("revocation left an active API credential", err)
	}
}

func waitForUserLock(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%FOR UPDATE%')", int32(blocker)).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("operation did not reach the user lock")
		}
	}
}

// Catches Unicode characters that case-fold into ASCII bypassing the username alphabet.
func TestRegistrationRejectsUnicodeCaseFold(t *testing.T) {
	s, _ := setup(t)
	if _, err := s.Register(context.Background(), "Kelvin", testPassword, "Kelvin"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("non-ASCII username accepted")
	}
}

// Catches cancellation being ignored while waiting for the global Argon2 budget.
func TestPasswordQueueCancellation(t *testing.T) {
	for i := 0; i < cap(passwordWorkers); i++ {
		passwordWorkers <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(passwordWorkers); i++ {
			<-passwordWorkers
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := hashPassword(ctx, testPassword); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued password work ignored cancellation", err)
	}
}

// Catches NULL semantics bypassing the active identity invariant in SQL CHECK constraints.
func TestDatabaseRejectsMissingActiveUsername(t *testing.T) {
	_, pool := setup(t)
	_, err := pool.Exec(context.Background(), "INSERT INTO account_users (id,username,display_name,password_hash) VALUES ('missing_username',NULL,'User','placeholder')")
	if err == nil {
		t.Fatal("active account accepted without username")
	}
}
