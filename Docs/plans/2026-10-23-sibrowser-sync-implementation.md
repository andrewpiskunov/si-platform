# SIBrowser Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Resumable full and daily import of SIBrowser SIQ files into the public catalogue.
**Architecture:** CLI discovers and downloads files, PostgreSQL checkpoints the work, existing API/ingest handles media and storage. A restricted source publication operation publishes playable imported revisions.
**Tech Stack:** Go, PostgreSQL, net/http, golang.org/x/net/html, existing ingest/library.
**Spec:** `Docs/plans/2026-10-23-sibrowser-sync-design.md` (approved).

## Global Constraints

- Source identity is global; retain receipts and do not resurrect deleted/withdrawn/blocked packages.
- No fabricated licence/rights confirmation. Download from our catalogue is disabled.
- Archive limit 250 MiB; default source request interval 1 second; bounded requests and retries.
- Full and incremental share one database lock; external HTTP never receives platform credentials.
- Only configured active admin owner can import/publish for the source. User quotas remain unchanged.

## Review Focus

- Crash after publication but before checkpoint: one public package and one decision only (Task 1/3).
- Loss of lock connection: cancel requests and fence checkpoint writes (Task 3).
- Source inserts at front during pagination: overlap and final head reconciliation (Task 3).
- Expired upload/temporary storage loss: retry same receipt with verified bytes, never duplicate (Task 1/3).
- Source returns login/HTML instead of archive: actionable non-success, never publish (Task 2).

### Task 1: External source import, publication and quotas

**Files:** migration 21; `internal/library/external_import.go`, `usage.go`, `service.go`, `catalogue_import.go`; `internal/ingest/external_import.go`, `catalogue.go`, quota users; `internal/server/external_import.go`, `authoring.go`.
**Interfaces:** `ingest.ExternalInput`; `CreateExternal(ctx, actor, input) (Upload,error)`; `library.PublishExternal(ctx, actor, source, externalID) (Card,error)`; shared DB-backed quota resolver.
- [x] Write integration tests proving source configuration, SHA receipt, public playable import, unknown licence, repeat, withdrawal, foreign owner and role revocation; run RED.
- [x] Implement migration/config, typed import, source-bound publication, preserved receipts and quota resolver. Explicit retry resets eligible failed upload with same ID, only before any pack exists.
- [x] Run ingest/library/server/themes tests with PostgreSQL; verify GREEN and commit.

### Task 2: Source parser and bounded downloader

**Files:** `internal/sibrowser/source.go`, `source_test.go`, fixtures, `go.mod`, `go.sum`.
**Interfaces:** `Source.Page(ctx,page) (Page,error)`; `Source.Download(ctx,item,directory) (file,sha,size,error)`; `Item{ID,Name,Author,SourceURL}`, `Page{Items,Next}`.
- [x] Write parser and HTTP tests: main list only, next links, invalid markup, hostile redirects/IP, cancellation, retry and archive bounds; run RED.
- [x] Implement structural HTML parser and secure HTTP client with bounded download to temp file, SHA and ZIP signature check. Classify inaccessible source/download responses without leaking signed URLs.
- [x] Run tests GREEN; commit.

### Task 3: Durable runner and API client

**Files:** `internal/sibrowser/{store,runner,api}.go` and tests; reuse catalogue upload transport where practical.
**Interfaces:** `Run(ctx,pool,Options) (Report,error)`; options include API credentials, source client, mode, scratch directory and limits; DB source config initialized by CLI.
- [x] Write PostgreSQL/HTTP tests for first run, repeat, checkpoint recovery, overlapping incremental pages, partial run, rejected items, source identity and lock exclusion; run RED.
- [x] Implement locked run, atomic discovery/checkpoint, immutable import inputs, retry queue, bootstrap/full and incremental overlap. Retry and publish use same durable source identity.
- [x] Run tests GREEN, including synthetic SIQ → real server → public catalogue; commit.

### Task 4: CLI, operations and final verification

**Files:** `cmd/sibrowser-sync/main.go`, tests, Dockerfile, compose.yaml, `.env.example`, Makefile, `Docs/35-SIBROWSER-SYNC.md`, README.
- [x] Write CLI validation tests for modes, missing credentials, invalid quotas and exit status; run RED.
- [x] Implement signal/timeout handling, source setup, JSON reports, image target, opt-in compose service and cron instructions. Document external robots restriction, VK dependency, full reconciliation, unsupported packages and operator retries.
- [x] Run all Go tests with PostgreSQL, relevant race tests, vet and build; check compose configuration and diff.
- [x] Fresh whole-branch review; fix important findings with regression tests; commit final changes and summarize actual verification and operational limitations.

## Execution record

- Baseline: `go test ./...` passed; PostgreSQL tests require explicit test DSN. Existing linked worktree confirmed.
- Execution follows approved design inline; no additional permission gate for already authorized implementation.

- Verification: PostgreSQL full Go suite passed; focused race suite passed; vet and Go command builds passed; Svelte check passed, frontend tests 107/107 passed on Node 22, frontend production build passed; Compose validated with explicit project name.

- Final review: three recovery defects reproduced and fixed: interrupted daily frontier, upload expiry classification, bounded full progress. All regression tests pass; real API test also expires an upload between lookup and PATCH and recovers with the same receipt.
- Final verification after fixes: full Go suite with PostgreSQL, focused race suite, vet, command builds, Docker image build and in-image CLI help passed. Actual SIBrowser HTML pages 1, 2 and 2288 parsed successfully; no bulk production import was run.
