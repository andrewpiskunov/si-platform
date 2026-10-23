# Package engagement implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Count and display downloads, 1–5 ratings and independent likes, preserving imported baselines.
**Architecture:** Source snapshot plus local counters and per-account votes. Public Card carries aggregate statistics; personal vote is session-only. Successful full public archive delivery increments the local counter.
**Tech Stack:** Go, PostgreSQL/pgx, Svelte 5.
**Spec:** Docs/plans/2026-10-23-package-engagement-design.md

## Global Constraints
- Sync remains stopped; never invent unavailable source counts.
- Existing publication gates and session authentication apply.
- Preserve imported/local separation and nullable source metrics.

## Review Focus
- Replayed/concurrent votes must not duplicate counts (task 1).
- Unknown source vote counts must not corrupt local average (task 1).
- Deleted accounts and withdrawn packs must not remain active (task 1).
- Failed/partial exports must not count (task 2).
- Source markup absence and recommendations must not create fake metrics (task 2).

### Task 1: Persistence and aggregate model
Files: migrations/00023_package_engagement.sql; internal/library/engagement.go, engagement_test.go, model.go, service.go; internal/privacy/data.go.
Interfaces: Engagement with source/local totals; Engagement(ctx,pack) and MyVote(ctx,actor,pack), SetVote(ctx,actor,pack,Vote) error, RecordDownload(ctx,pack) error.
- [x] Write and run failing integration tests for vote upsert/removal, concurrent changes, weighted source mean and unknown count, publication/auth gates.
- [x] Implement schema, methods, public card projection, source nullable fields, privacy export.
- [x] Run Go library/privacy tests against isolated PostgreSQL; expected PASS.

### Task 2: HTTP and import
Files: internal/server/engagement.go, authoring.go, files.go, engagement_test.go; internal/sibrowser/metadata.go, metadata_test.go; internal/library/source_metadata_model.go.
Interfaces: GET/PUT /catalogue/{id}/vote, payload {rating:null|1..5,liked:boolean}; Card.engagement.
- [x] Add failing parser tests for scoped AggregateRating microdata and missing values; HTTP tests for auth/idempotence/download failure or ranges.
- [x] Implement HTTP handlers, completed full-download recording, scoped optional source parsing/validation.
- [x] Run library/server/sibrowser tests; expected PASS.

### Task 3: UI, verification and review
Files: web/src/lib/PackEngagement.svelte, engagement.ts, engagement.test.ts, PackCard.svelte, types.ts; web/src/routes/packs/[id]/+page.svelte.
- [x] Add display helper tests for empty/local/combined/unknown-source ratings; run RED.
- [x] Render three metrics in catalogue and detail; editable session-only ratings/like with error feedback and pending state.
- [x] Run frontend tests/check/build, Go tests, review diff and independent whole-change review; expected PASS.
- [x] Deploy local API image and verify browser; leave sync stopped. Record actual source limitations and verification in this plan.

## Verification — 23 October 2026

- Full `go test -p 1 ./...` with TEST_DATABASE_URL against isolated schemas: PASS.
- Focused `go vet` (library/server/sibrowser/privacy): PASS.
- Frontend: 112 tests PASS, svelte-check 0 errors/0 warnings, production build PASS.
- Independent review: fixed owner downloading current unpublished revision through public endpoint; fixed counting before buffered-response flush. Both reproduced RED and passed GREEN regression tests.
- Browser: desktop 1280 and mobile 390, no horizontal overflow/errors; create/change/remove rating and like/unlike verified against local API. Temporary test votes removed and test account deleted.
- Docker image `si-platform-engagement:local`, migration 23 applied; local API ready on 8081, UI on 5174. Stopped full worker recreated with new binary, never started; checkpoint 7 unchanged.
- Source limitations: existing 23 snapshots have downloads but no rating/like values. Parser supports explicit scoped schema.org AggregateRating (1–5) and LikeAction interaction counts; synthetic tests establish the contract, not live source availability. Sync remains stopped.
- Counting policy: successful complete full public SIQ responses, not unique people; HEAD, range/resume, failed writes/flushes and draft exports excluded.
