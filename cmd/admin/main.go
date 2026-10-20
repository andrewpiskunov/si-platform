// Admin is a local operator-only CLI. Database access is required; no role
// assignment endpoint is exposed to website clients.
package main

import (
	"context"
	"fmt"
	"os"
	"siplatform/internal/storage/postgres"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 4 || os.Args[1] != "role" {
		return fmt.Errorf("usage: admin role <username> <author|moderator|admin>")
	}
	role := os.Args[3]
	if role != "author" && role != "moderator" && role != "admin" {
		return fmt.Errorf("invalid role")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	defer tx.Rollback(context.Background())
	var id string
	err = tx.QueryRow(ctx, "UPDATE account_users SET role=$2 WHERE username=$1 AND deleted_at IS NULL RETURNING id", strings.ToLower(os.Args[2]), role).Scan(&id)
	if err != nil {
		return fmt.Errorf("active account not found or database unavailable")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO account_audit(user_id,action,target_id) VALUES($1,$2,'operator_cli')", id, "role_"+role); err != nil {
		return fmt.Errorf("audit unavailable")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("role update not confirmed")
	}
	fmt.Printf("Account %s role: %s\n", id, role)
	return nil
}
