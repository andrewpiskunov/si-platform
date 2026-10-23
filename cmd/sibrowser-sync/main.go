// sibrowser-sync is an operator CLI; run it from cron, not from an HTTP handler.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"os/signal"
	"siplatform/internal/sibrowser"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
func run(ctx context.Context, args []string, getenv func(string) string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("sibrowser-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "incremental", "full, incremental or metadata (existing packs only)")
	dir := fs.String("directory", getenv("SI_SYNC_DIR"), "scratch directory (default .local/sibrowser)")
	pages := fs.Int("max-pages", 0, "maximum forward pages this run, excluding up to 2 resume overlap pages (0 unlimited)")
	items := fs.Int("max-items", 0, "maximum packages attempted this run (0 unlimited)")
	timeout := fs.Duration("timeout", 23*time.Hour, "overall deadline; increase for initial full import")
	interval := fs.Duration("interval", time.Second, "minimum source request interval, at least 1s")
	insecure := fs.Bool("allow-http-api", false, "allow cleartext API on a trusted private container network")
	forceMetadata := fs.Bool("force-metadata", false, "refresh metadata regardless of age")
	retry := fs.Bool("retry-rejected", false, "explicitly retry rejected entries under their existing receipt")
	owner := fs.String("configure-owner", "", "configure source account ID and quotas, then exit")
	count := fs.Int("pack-limit", 0, "source owner's total pack quota, required for configuration (1..100000)")
	storage := fs.Int64("storage-limit-bytes", 0, "source owner's total logical storage quota, required for configuration")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || (*mode != "full" && *mode != "incremental" && *mode != "metadata") || *pages < 0 || *items < 0 || *timeout <= 0 || *interval < time.Second || getenv("DATABASE_URL") == "" {
		fmt.Fprintln(stderr, "invalid_sync_configuration")
		return 2
	}
	if *owner != "" {
		if *count < 1 || *count > 100000 || *storage <= 0 || *storage > 1<<50 {
			fmt.Fprintln(stderr, "explicit_valid_quotas_required")
			return 2
		}
	} else if getenv("SI_API_URL") == "" || getenv("SI_API_TOKEN") == "" || *count != 0 || *storage != 0 {
		fmt.Fprintln(stderr, "api_url_and_token_required")
		return 2
	}
	if *dir == "" {
		*dir = ".local/sibrowser"
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cfg, e := pgxpool.ParseConfig(getenv("DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(stderr, "invalid_database_configuration")
		return 2
	}
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	cfg.MaxConns = 4
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		fmt.Fprintln(stderr, "database_unavailable")
		return 1
	}
	defer pool.Close()
	if *owner != "" {
		if e = sibrowser.Configure(ctx, pool, *owner, *count, *storage); e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		fmt.Fprintln(out, `{"configured":true,"source":"sibrowser"}`)
		return 0
	}
	report, e := sibrowser.Run(ctx, pool, sibrowser.Options{Mode: *mode, APIURL: getenv("SI_API_URL"), Token: getenv("SI_API_TOKEN"), Directory: *dir, InsecureAPI: *insecure, MaxPages: *pages, MaxItems: *items, RetryRejected: *retry, ForceMetadata: *forceMetadata, Source: sibrowser.NewSource(*interval)})
	if encodeErr := json.NewEncoder(out).Encode(report); encodeErr != nil {
		fmt.Fprintln(stderr, "report_write_failed")
		return 1
	}
	if e != nil {
		code := "sync_failed"
		var f *sibrowser.Fault
		switch {
		case errors.Is(e, sibrowser.ErrConfig):
			code = "invalid_sync_configuration"
		case errors.Is(e, sibrowser.ErrBusy):
			code = "source_busy"
		case errors.Is(e, sibrowser.ErrIncomplete):
			code = "sync_incomplete"
		case errors.Is(e, context.Canceled):
			code = "cancelled"
		case errors.Is(e, context.DeadlineExceeded):
			code = "deadline_exceeded"
		case errors.As(e, &f):
			code = f.Code
		}
		fmt.Fprintln(stderr, code)
		if errors.Is(e, sibrowser.ErrBusy) {
			return 3
		}
		if errors.Is(e, sibrowser.ErrConfig) {
			return 2
		}
		return 1
	}
	return 0
}
