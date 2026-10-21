# Комнаты и общие правила — план реализации

> **For agentic workers:** superpowers:executing-plans для ledger/TDD/приёмки; superpowers:dispatching-parallel-agents для независимых game/web модулей. Один свежий обзор всего этапа, одна коррекция.

**Goal:** закрыть публичность/поиск комнат, внешнюю голосовую ссылку и all-role правила FR-02/09/13.
**Architecture:** настройки в durable snapshot, производный индекс PostgreSQL; обычная идемпотентная команда; явный публичный DTO; существующий Svelte/WS.
**Tech Stack:** текущие Go/pgx/sqlc/PostgreSQL18/SvelteKit/Chromium.
**Spec:** [Docs/18-ROOMS-AND-RULES.md](../18-ROOMS-AND-RULES.md).

## Global Constraints

- 12 игроков/20 зрителей; новый игрок только между вопросами. Host transfer сохраняет полномочия. Не менять игровую логику ставок/медиа.
- Settings value struct `game.RoomSettings {Visibility string, VoiceURL string}`; `Normalized() RoomSettings`, `Validate() error`; State/View.Settings value, JSON settings. Payload `{settings:...}`. Оmitted create settings default; command требует explicit visibility.
- Список DTO/пагинация/ограничения — точно по spec. No-store, никаких участников/пакетов/голоса/секретов, без actor allocation.
- public→private отключает прежнее приглашение в command transaction; новый секрет только существующим rotate endpoint. Replay не отключает созданное позднее приглашение.
- Никаких дополнительных внешних сервисов/платежей; страницы сохраняют оформление ?room=; никакого автоjoin.

## Review Focus

1. Публичный список и join на границе смены доступа; default старых snapshot, индекс/снимок не расходятся, список не раскрывает лишнее.
2. Повтор settings-команды после выпуска новой ссылки, transfer host, неправильный seq, HTTP/WS ошибки и старые клиенты.
3. URL с userinfo, управляющими символами/обратной косой чертой; голос не загружается сервером/браузером автоматически.
4. Уведомление о правилах после reconnect, сохранение несохранённой формы, скрытые ответы не появляются в журнале.
5. Список/пагинация с исчезнувшими комнатами, отказ сетевого запроса, узкая навигация обычного пользователя/модератора.

## Task 1 — Pure game contract (independent worker)

**Files:** только internal/game, включая settings.go/test и существующие state/v2_command/v2_engine/v2_projection.
**Interfaces:** RoomSettings/Normalized/Validate/SettingsPayload{Settings RoomSettings}; State/View.Settings; CanJoinRole(State,Role) bool (same limits/phase as AddParticipant); `set_room_settings` с expected_seq. Не менять storage/web.

- [x] RED тесты default/projection, URL validation, all role/phase/seq gates, legacy behavior, CanJoinRole limits; `go test ./internal/game` ожидаемый RED absent types/behavior.
- [x] Implement минимальный переход и allowed action в lobby/paused; frozen gameplay/rules не меняются; settings в legacy projection тоже. Не расширять допустимость set_rules.
- [x] Full `go test -race ./internal/game` → GREEN; return файлы и факты без отдельного commit/reviewer.

## Task 2 — PostgreSQL и HTTP (root)

**Files:** migration11/sqlc, storage/postgres settings/list/tests, store/game_setup/commands/room_controls/game_clock, server play/server/room_controls/tests.
**Consumes:** Task1 RoomSettings; общий CanJoinRole. **Produces:** GET rooms exact DTO, create.settings, public join, rules_change events.

- [x] RED integration: default migration/public list privacy + limits/cursor, public/private admission, closure/invite replay/retained credential, role/phase/transfer/seq, journal rules change; real PostgreSQL.
- [x] Add generated column/index and regenerate sqlc; defaults/validate creation/Load; public DTO query with bounded scan; locked Join decision; invalidate invite only accepted transition, duplicate path unchanged; explicit rules delta.
- [x] RED HTTP create/list/join/cache/invalid inputs using existing fixtures, implement routes/fields/error mapping.
- [x] Focused then full relevant `go test -race ./internal/storage/postgres ./internal/server ./internal/game`, vet → GREEN, scoped commit including Task1.

## Task 3 — Web UI (independent worker)

**Files:** web only, room types, CreateRoom, GameV2/Rules/Results, room route, new rooms index SSR+page, layout/CSS as needed; helper tests. Не менять Go/e2e, не перезапускать сервисы.
**Consumes:** contract spec. Settings through RoomClient.send v2; no new HTTP settings endpoint. Rules journal PublicEvent optional rules_change.

- [ ] RED unit helper tests version observation across reconnect/tab reload/storage denial, dirty forms preserved/explicit reset; safe public list query/cursor as useful.
- [ ] Add creation visibility/voice controls and host lobby/paused editor, all-role summary and voice link. Dirty forms never silently reset; stale conflicts remain explicit. Use existing action availability and pending command handling.
- [ ] Add /rooms list, no autojoin, separate guest player/spectator entry without invite; existing private fragment flow retained. Old invitation cleared/warning on public→private; existing rotate button issues fresh link. Accessible rules notice plus result rendering.
- [ ] `npm test`, `npm run check`, `npm run build` → GREEN; no own reviewer/commit.

## Task 4 — Integration and acceptance (root)

**Files:** e2e/rooms.mjs/package.json, README/Docs11/13/18/plan, integration fixes.
**Consumes:** Tasks1–3, exact labels obtain from worker.

- [ ] RED→GREEN Chromium full settings/list/join/invite/rules/voice/dirty/reconnect flows with real API and synthetic users. Verify no voice request and no page errors; 390/820/1440 overflow checks.
- [ ] Fresh full Go race with DB/MEDIA, vet/build; web full tests/check/build; all existing7 e2e plus rooms. Restart only owned API/Vite, check readiness.
- [ ] One fresh whole-stage reviewer on strongest model with spec/plan/ledger/package; Important/Critical one RED→GREEN correction pass and relevant/full suite; record rulings and deferred limitations.
- [ ] Update actual docs/status, commits, FF local main, continue guest removal/ban/history and remaining P0 without stopping for authorization.
