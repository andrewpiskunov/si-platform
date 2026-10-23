# SIBrowser Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Import, refresh and display complete SIBrowser package metadata and locally stored covers, including existing imports.
**Architecture:** Separate source snapshots and cover references from immutable SIQ receipts/revisions. A lease-fenced metadata runner calls narrow authenticated API methods, uploads covers through existing media ingest, and exposes only publication-gated public projections.
**Tech Stack:** Go, PostgreSQL, existing media sandbox/blob store, Svelte/TypeScript.
**Spec:** `Docs/plans/2026-10-23-sibrowser-metadata-design.md` (approved).

## Global Constraints
- Full stays stopped; no cron installed or new SIQ bulk import during verification.
- Preserve source/IP/redirect/size safeguards and never send platform credentials to source.
- Source metadata updates never change SIQ bytes, game revisions, publication state or bootstrap checkpoint.
- Public covers/preview follow publication, deletion and blocking rules; no answer/host-comment leakage.
- Local images use existing sandbox and object storage; quota, retention and privacy exports include new references.
- Default metadata freshness 24h; metadata/cover failures retain good data and retry independently.

## Review Focus
- Recommendations nested in the main article and repeated mobile stats must not contaminate metadata (Task 1).
- Missing fields differ from valid zero percentages and malformed pages (Task 1).
- Revocation/withdrawal during metadata or cover upload must not expose content (Task 2).
- Cover replacement/failed upload and quota accounting must survive retention (Task 2).
- Forced metadata runs after interruption must not loop forever or advance SIQ discovery (Task 3).

### Task 1: Snapshot contract and parser
Files: new `internal/library/source_metadata_model.go`, `internal/sibrowser/metadata.go`, `internal/sibrowser/metadata_test.go`, fixtures in `internal/sibrowser/testdata/`; modify `source.go` author aggregation.
Interfaces: `library.SourceMetadata` (name, authors links, description paragraphs/links, tags links, date, filename/size label, question count, downloads, categories/media percentages, original links, cover URL, round/theme/question preview); `(*Source).Metadata(ctx,id)` and `Cover(ctx,id,url)` bounded bytes.
- [x] Write parser tests for two authors, absent cover, unrelated nested articles, duplicated stats, question previews without answers, malformed page.
- [x] Run focused tests RED; implement typed bounded contract and main-article-only parser; use real HTML fixtures plus synthetic hostile cases.
- [x] Run focused tests GREEN and verify current live sample cards read-only.

### Task 2: Durable metadata, API and covers
Files: new migration 22; new library/source_metadata.go, server/source_metadata.go and tests; modify library card projection/search, usage, blobrefs, retention, privacy.
Interfaces: `SaveSourceMetadata(ctx,actor,id,epoch,metadata)`, `AttachSourceCover(ctx,actor,id,epoch,uploadID,coverURL)`, publication-gated `PublicSourceMetadata`, `PublicCover`; compact Card fields for metadata and cover URL. Source covers attach only ready image uploads from the same owner/credential, with reservations consumed atomically, never attach a new game revision.
- [x] Tests RED: API authorization, epoch fencing, unchanged revision/receipt, source search/tags, safe public preview/cover and withdraw; cover idempotency/quota/retention.
- [x] Implement snapshot table linked to pack, compact API projection and full detail endpoint, validated cover attachment through existing media uploads. Retention queues obsolete objects; quota counts metadata and image assets; privacy export includes snapshot and references.
- [x] Tests GREEN with real isolated PostgreSQL schemas.

### Task 3: Metadata sync and CLI
Files: new sibrowser/metadata_runner.go and tests; modify runner.go, store.go, api.go, CLI and its tests.
Interfaces: `--mode=metadata`, `--force-metadata`, existing max-items/deadline/interval. Queue selection by due metadata/cover dates, common lease, per-run keyset/visited boundary; source state unchanged. Snapshot and cover results reported separately.
- [x] Tests RED: metadata-only with no SIQ requests and unchanged checkpoint, interrupted/forced resume, transient cover failure while metadata updates, suppression and stale epoch.
- [x] Implement backfill, retry and 24h refresh; new imports attempt optional metadata after publication; incremental refreshes encountered old cards plus due failed metadata tasks.
- [x] Focused CLI/runner integration tests GREEN.

### Task 4: Catalogue and package UI
Files: types.ts, PackCard.svelte, new SourceDetails.svelte, package load/page, CSS, frontend tests.
- [x] Add test for percent formatting, empty vs zero, merged tags and safe links where helper logic needs coverage.
- [x] Render local cover with fallback, all authors/date/source downloads in list; full source fields, SIQ details, category/media percentages, rounds/themes and collapsed question preview on detail page. Fetch large details only there. External links use safe URLs and noreferrer.
- [x] Frontend check/build/tests, desktop/mobile browser checks and publication visibility checks.

### Task 5: Review and local backfill
Files: Docs/35-SIBROWSER-SYNC.md and design/plan status.
- [x] Run Go tests using isolated test DB, relevant race tests, vet, frontend validation, Docker build and actual sandbox readiness.
- [x] Independent whole-change review, fix material findings and rerun affected tests.
- [x] Update local API/frontend without starting full; metadata-only backfill existing imported packs; verify visible fields and images through actual HTTP/browser, report unsupported/missing covers honestly.
- [x] Commit verified implementation and record exact checks and live results. Full container remains exited.

## Verification record — 2026-10-23

- All Go packages passed with real isolated PostgreSQL schemas (`go test -p 1 ./...`). One initial parallel run hit the existing host/DB-time-sensitive room migration assertion; isolated reproduction and complete serial run passed without changing that test.
- Focused library/sibrowser race tests passed; `go vet ./...` passed.
- Svelte check: 0 errors/0 warnings; Node 22 production build and 109 frontend tests passed.
- Independent review: five important findings reproduced and fixed (initial cover, source byte changes on resume, independent cover retry, quota transfer, private revision projections). Metadata refresh after game edits also preserved. Tests cover retention of consumed uploads and removed-cover GC candidates.
- Docker image built; Linux `TestRealSourceCoverSandbox` passed through real HTTP, ingest, FFmpeg/bubblewrap, blob storage and public cover endpoint; withdrawal returns 404.
- Local API/frontend updated. Metadata-only pass: 23 snapshots, zero errors, zero new packs, checkpoint remains 7. All 23 source cards lack covers; fallback stays textual. Full container updated to new image but not started.
- Browser: desktop and 390px mobile detail/catalogue checks, no horizontal overflow or browser errors; preview collapsed by default and expands after clicking its summary.
