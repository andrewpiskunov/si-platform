# Membership and revocation implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Checkbox steps track actual completion. Root coordinates scoped independent game/web workers, owns storage/server/integration; one fresh whole-stage review, one correction pass, no per-task review loop.

**Goal:** Удаление/бан браузерной сессии с полным отзывом доступа и сохранением игровой истории.
**Architecture:** Active membership separates authorization from frozen game references. Room-locked transaction owns bans, session revocation and bounded live snapshot; durable roster owns history. Existing actor evicts clients before subsequent projection.
**Tech Stack:** Existing Go/chi/pgx/sqlc/goose, SvelteKit, PostgreSQL; no new product dependency.
**Spec:** Docs/19-MEMBERSHIP-AND-REVOCATION.md.

## Global constraints

- 12 players + host +20 spectators; no lifetime join cap; snapshots remain below existing 64 KiB transport limit for supported states.
- No IP/account ban, paid services or public deployment. Browser-session evasion explained honestly.
- Account → assets → engine owner → room lock order. Every authorization checks active membership. Old snapshots default removed=false.
- v2 controls; legacy remains readable. Retain frozen bets/answers/random outcomes/final order. Prune only after archive persistence, return same snapshot as saved.
- Native continuing execution already authorized. Plan/spec self-review inline; external prerequisites remain honestly unverified.

## Review focus

1. Removal commit succeeds but response fails: no subsequent snapshot/file grant to removed identity, duplicate repairs eviction.
2. Ban targets another tab of host browser: reject entire group, do not revoke current host.
3. Removed player already wagered/answered/won random selection: retain decision and finish frozen final despite fewer than two active players.
4. Repeated kick/rejoin with long names: snapshot and roster page bounded; names/scores available in history after pruning.
5. Browser denies sessionStorage: new host keeps access/recovery in memory, revoked guest cannot reconnect/replay or automatically rejoin.

## Task 1 — Pure game membership (scoped worker)

**Files:** internal/game/{state,v2_command,v2_engine,v2_projection,v2_question,v2_final,v2_media,v2_rules}.go as needed; new membership.go/membership_test.go. No storage/server/web edits.
**Produces:** Participant.Removed bool omitempty; State.ActiveParticipant(id) (Participant,bool); RemovalPayload{TargetID string `target_id`, Mode string `mode`}; ApplyRemovalAt(s State, actorID string, c Command, targets []string, nowMS int64) (State,error); PruneParticipants(s State) State.
**Consumes:** trusted targets supplied only by storage; validate payload/mode/seq/host in pure transition. ApplyAt supports kick with singleton target; ban requires explicit ApplyRemovalAt resolved group from storage, never accepts IDs from JSON. Command validation accepts both modes with strict payload.

- [ ] RED tests missing removal, host/self/finished/stale/unauthorized, ban containing host atomic denial; old snapshots normalize active; freed seats/new zero score.
- [ ] Implement ActiveParticipant and filtering at authority/admission/new player selection; retained raw Participant for frozen references. Add host allowed action non-host targets. Projection for inactive identity reveals no gameplay/question/actions.
- [ ] RED frozen auctions/random candidates/secret/final bids+answers/maxima/corrected score and readiness tests. Implement pause on active-stage player removal, preserve frozen data, unavailable readiness, final minimum fix and safe deterministic chooser repair.
- [ ] RED prune repeated removal preserves active/current question/final/legacy/current last20 decisions+appeals references, drops irrelevant removed. Implement pure PruneParticipants, no archive side effects.
- [ ] Run `go test -race ./internal/game` and `go vet ./internal/game`; report RED/GREEN evidence/files/signature caveats. Root commits, no worker review/service restart.

## Task 2 — Atomic storage and history (root, after Task1 interfaces)

**Files:** migrations/00012_room_membership.sql, sql/queries.sql + generated db; postgres membership.go/tests, store.go, commands.go, room_controls.go, public_rooms.go, state-saving call sites.
**Produces:** existing Create/Join compatibility wrappers plus CreateGameWithGuest(ctx,name,setup,guest string), JoinWithGuest(ctx,roomID,invite,name,role,guest string); guest is opaque cookie validated/hash inside store, not a hash supplied by HTTP user. BindGuest(ctx,roomID,token,guest string) error; Roster(ctx,roomID,actorID,after string,limit int) (RoomRoster,error).

- [ ] RED real PG migration backfill/down/reapply, bind NULL once/no rebind/cookie not credential, kick/ban/replay/group with host and locked admission race.
- [ ] Implement migration/queries; archive initial host and deltas on each state persistence. Refactor saveState to accept *State so pruning changes same returned next. Batch archive upsert, never reinsert an archived removed user as active. Authenticate excludes revoked session; all store actor checks use ActiveParticipant before receipts.
- [ ] Resolve trusted ban set inside room lock; run game.ApplyRemovalAt and delete revoked sessions/add ban atomically with state/event/receipt. Preserve digest for removed target through banned-record creation before deleting sessions. Do not acquire account locks afterward.
- [ ] RED history tests paginated roster/cursor/private identity denial and results active+event-referenced names after pruning, many rejoins bounded snapshot. Implement safe removed_ids/chooser_change deltas and result roster lookup; active public counts.
- [ ] Run focused then full postgres/game real DB race, migration tests and vet; generate pinned sqlc via Go if make absent; scoped commit incl Task1.

## Task 3 — Transport/cookie/revocation (root)

**Files:** server guest_sessions.go/membership.go/tests, play.go, room.go, connection.go, room_controls.go, game_v2.go, media_delivery.go, appearance.go and server routes.
**Produces:** room-scoped si_guest cookie and POST /api/rooms/{id}/session; GET roster DTO; terminal unauthorized on revoked clients when deliverable. Commands retain existing WS envelope.

- [ ] RED real HTTP/WS tests normal+appearance removal, preauthenticated attach, queued command, media GET/HEAD/Range tickets, board/results/theme denial; public and private ban reentry.
- [ ] Issue cookie at successful create/join/bootstrap; reuse valid64hex otherwise random32bytes, never log/return hash. Secure from existing trusted HTTPS configuration; HttpOnly/Strict/30days/path scoped. Bootstrap live token and no identity rebind.
- [ ] Implement actor eviction before broadcast, remove buckets, active checks every authorization surface, reconcile ambiguous removal commit before any later delivery and duplicate recovery; close fail-closed on reload error. Preserve existing failed-host-pause ordering.
- [ ] RED commit/read fault injection, host transfer/current-host ban-group rejection, stale token and cookie-only. Implement explicit contextual errors, roster endpoint/no-store.
- [ ] Run server race with disposable DB; full Go race/DB/media, vet/build, restart own API after migration12.

## Task 4 — Browser controls and credential lifecycle (scoped worker, independent after contract)

**Files:** web/src/lib/{types,room-client,game-v2,GameV2,GameResults,CreateRoom}.ts/.svelte; rooms/[id]/+page.svelte; new RoomParticipants.svelte/helpers/tests. No Go/e2e/service edits.
**Consumes:** allowed_action remove_participant targets, payload target_id/mode, removed?:boolean, roster GET, terminal unauthorized, POST session bearer-auth bootstrap. Results optional removed_ids/chooser_change. Server remains final authority for ban grouping.

- [ ] RED unit revocation clears pending/credential and stops reconnect/replay; storage denial retains new host credential in memory. Implement shared memory+best-effort sessionStorage credential helper and create/join/reload use; no credential in URL/query/log. Room session bootstrap before WS, no unbounded failure retry.
- [ ] Implement host target selector/two explicitly confirmed controls «Удалить участника»/«Запретить вход этой сессии», host excluded, anonymous limitation text. Read-only removed history and paginated roster, existing scores maintain active/removed distinction. No automatic rejoin.
- [ ] Contextual join401 requests a fresh invite instead of account login. Terminal removal releases theme/media, clears pending/storage and shows explicit exit/rejoin affordance.
- [ ] RED helper tests active target filtering, pending failure and denied storage; `npm test`, check/build. Root commits and integrates, no worker reviewer/runtime mutation.

## Task 5 — Acceptance and integration (root)

**Files:** e2e/membership.mjs + package.json, Docs11/19/13/README, ledger.

- [ ] Real Chromium kick/rejoin zero score, ban across browser tabs/other room unaffected, host-group rejection, active removal paused/frozen outcomes, stale media/appearance/credentials stop, roster pagination and disabled storage host; keyboard/mobile390/820/1440.
- [ ] Full Go race with DB/media, web tests/check/build, all9 browser scenarios. Assert no page errors and no secret/hash in public data. Fresh source builds/services only, tests use synthetic own data.
- [ ] One fresh strongest-model whole-stage review with spec/plan/ledger/base..head. One Important/Critical correction pass with regression; rule on every declined-to-judge; defer Minor explicitly, no re-review.
- [ ] Update actual status, commit/FF main, continue remaining P0 moderation/quotas/privacy/AI/MCP/import/operations. Do not claim complete product or external verification.
