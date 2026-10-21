package main

import (
	"context"
	"net/url"
	"os"
	"siplatform/internal/accounts"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"strings"
	"testing"
)

func TestRoleAssignmentCommitsWithAudit(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	auth := accounts.New(pool, accounts.Options{})
	session, err := auth.Register(ctx, "operator_target", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	dsn := pool.Config().ConnString()
	schema := pool.Config().ConnConfig.RuntimeParams["search_path"]
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("DATABASE_URL", dsn)
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, role := range []string{"moderator", "admin", "author"} {
		os.Args = []string{"admin", "role", "OPERATOR_TARGET", role}
		if err = run(); err != nil {
			t.Fatalf("assign %s: %v", role, err)
		}
		var stored, action, target string
		err = pool.QueryRow(ctx, `SELECT u.role,a.action,a.target_id FROM account_users u JOIN account_audit a ON a.user_id=u.id WHERE u.id=$1 ORDER BY a.id DESC LIMIT 1`, session.User.ID).Scan(&stored, &action, &target)
		if err != nil {
			t.Fatal(err)
		}
		if stored != role || action != "role_"+role || target != "operator_cli" {
			t.Fatalf("assignment/audit diverged: %s %s %s", stored, action, target)
		}
	}
	os.Args = []string{"admin", "role", "operator_target", "owner"}
	if err = run(); err == nil {
		t.Fatal("unknown role accepted")
	}
}
