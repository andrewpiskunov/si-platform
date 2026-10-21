# Moderation completion implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL superpowers:executing-plans. Root owns API/integration; independent scoped library and web workers after contract. One fresh whole-stage review and one correction pass; no per-task reviews.

**Goal:** Автор видит причины решений; модератор проверяет медиа и закрывает исторические жалобы.
**Architecture:** Existing publication transactions, additive terminal report metadata, bounded private history/report pages; reused pinned media preview.
**Tech Stack:** Go/chi/pgx/goose/PostgreSQL, SvelteKit; no new dependency.
**Spec:** Docs/20-MODERATION-COMPLETION.md.

## Global constraints

- Existing account-before-pack lock order; resolution takes actor then report, never later pack. Refresh live session/role before actions and idempotent response.
- Owner/mod decision history contains no reporter identity. API token cannot moderate.
- Report status open/resolved; disposition empty/decision/resolved/dismissed/legacy. Legacy attribution/time remain unknown.
- Pages<=50; no externalmedia/autoplay; strict version pinning. No public deployment/paid services.

## Review focus

1. Revoked/demoted moderator retries already-accepted resolution: still denied.
2. Old report after new publication/withdrawal/deletion: resolve/dismiss works; historic block works while pack active.
3. Concurrent explicit resolution and packblock: exactly one terminal metadata, no overwritten reason.
4. Old preview completes after new selection: oldmedia never shown undernewtitle.
5. Refresh failure after action: existinglist/reason retained, retry local; no sitewide500.

## Task1 — Durable decisions/report resolution (library worker)

Files migrations/00013_report_resolution.sql, internal/library/{moderation.go,model.go,publication.go,moderation_test.go}, existing publication tests.
Interfaces DecisionHistory(ctx,actor,packID,afterID int64,limit int)(DecisionPage,error); ReportPage(ctx,actor,status,after string,limit int)(ReportsPage,error); ResolveReport(ctx,actor,reportID,disposition,reason string)(Report,error). DecisionPage Items []DecisionRecord NextAfterID int64 omitempty. ReportsPage Items []Report NextAfter string omitempty. Preserve existing Reports wrapper semantics for internal compatibility; HTTP adopts bounded new method.
- [x] RED realDB history owner/stranger/mod/token scope, pages/cursors; terminal same/conflict/liveauth and rolechange.
- [x] Migration adds terminal fields/checks/indexes, legacybackfill/down/up. ExtendReport DTO; implement bounded history and report queries plus immutable resolution with actor+reportlocks.
- [x] RED historicblockafternewpub/withdraw, staleapprove/reject, allopenclosedbutalreadyresolvedunchanged, deletedownerresolution, concurrentblock/resolve. Implement blockexistingrevision precondition and open-only auditupdates.
- [x] Run library race/vet + migration tests, inspect DB constraints and lock order; scopedcommit.

## Task2 — HTTP API (root)

Files internal/server/{authoring.go,moderation.go,moderation_test.go}.
- [x] RED strictJSON limit/cursor/bodyvalidation, privateACL/role/tokendenial/revocation; history/reportDTO pagination and no authorreporterexposure.
- [x] Register /drafts/{id}/decisions, /reports/{id}/resolution, reportsstatus/cursor parsing. Existing identity/appError/no-store conventions; service owns enforcement. UnknownJSON/body rejected.
- [x] Full Go race DB/media, vet/build; migration13 localpreview, restartonlyownedAPI.

## Task3 — Author/moderator workflows (independent web worker)

Files web/src/lib/{types.ts,ModerationHistory.svelte,moderation-model.ts,moderation-model.test.ts}, web/src/routes/{moderation/+page.svelte,studio/[id]/+page.svelte}. No Go/e2e/runtime edits.
- [x] RED model validation/page merge/selectedrevision/terminalaction guards; implement UIlabels/types after sharedspec.
- [x] Add separate private decisionhistory author+moderator with boundednextpage; show reasons/version/date, no fabricated legacyactor/time.
- [x] Report open/resolvedfilter+pagination, perreportreason, explicitdismiss/resolve/block, mutationerrorpreservesdraft/list; localreload independent queue/reports.
- [x] Reuse keyedQuestionPreview for one selectedquestion, show pinnedrevision/history; explicitmedia only; request generation/abort prevents stale preview or list races.
- [x] npm test/check/build; report evidence; no commit/review/service restart.

## Task4 — Integrated acceptance (root)

Files e2e/moderation.mjs/package.json, Docs11/20/README, ledger.
- [x] Chromium authorpublish→modinspectimage/audio/videoanswerfragment→rejectreasonvisible→resubmitpublish→report→dismiss and resolve/oldrevisionblock; filterpages, latepreview/failure/mobile, no pageerrors. Synthetic own data cleanup.
- [x] Full relevantGo/web/allbrowser suite. Fresh whole-stage strongestreview, singleImportant/Criticalfixpass; rule on everydeclined-tojudge, no re-review.
- [x] Update factualstatus and scopedcommit/FFmain; continue quota/privacy P0.
