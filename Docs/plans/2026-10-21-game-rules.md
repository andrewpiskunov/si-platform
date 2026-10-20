# Полные текстовые игровые правила — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans; независимые задачи выполнять по superpowers:dispatching-parallel-agents с указанным владением файлами. Один общий review после интеграции.

**Goal:** Провести в браузере полный текстовый пакет P0 с обычными и специальными вопросами, финалом, таймерами и управлением ведущего.
**Architecture:** Версия 2 расширяет сохранённый снимок комнаты полем `gameplay`; v1 остаётся совместимой. Детерминированный движок принимает серверное время/энтропию от хранилища; WS передаёт только проекцию роли. Пакет компилируется на сервере из закреплённой версии.
**Tech Stack:** Go, PostgreSQL/pgx/sqlc, coder/websocket, chi; SvelteKit/TypeScript.
**Spec:** [Docs/14-GAME-RULES.md](../14-GAME-RULES.md), [Docs/02-REQUIREMENTS.md](../02-REQUIREMENTS.md).

## Global Constraints

- 1 ведущий, 2–12 игроков, 0–20 зрителей; 2000 вопросов в пакете; команды WS ≤16 KiB, исходящие сообщения ≤64 KiB.
- Таймеры 5–120 с; defaults 30/10/30/30/60 с; случайное окно 100–300 мс; фальстарт 1–5 с, default3.
- Подтверждение после commit; receipt lookup раньше текущего этапа; неподдержанные параметры не превращаются молча в обычный вопрос.
- Только серверное время и случайность. Приватные данные не попадают в чужую проекцию или журнал HTTP.
- Медиа, ИИ, свои ZIP-темы и нагрузочные испытания остаются последующими P0, а не объявляются выполненными этим планом.

## Review Focus

1. Повтор команды после перехода/смены правил: тот же результат без повторных очков — storage regression.
2. Отключение бывшего ведущего после передачи: новая игра не приостанавливается ошибочно — server regression.
3. Низкий/равный счёт, ва-банк и крайние секретные диапазоны: правильный допуск и ограничения — core table tests.
4. Перезапуск между первым кандидатом и выбором случайного победителя: ни повторного выбора, ни потери закрытых данных — core+storage tests.
5. Неполные ответы, истёкший срок и переход вкладки: UI остаётся управляемым, закрытые данные не раскрываются — browser+projection tests.

## Task 1 — Движок и компиляция (владелец internal/game/**, internal/playpack/**)

Создать `v2_types.go`, `v2_command.go`, `v2_rules.go`, `v2_question.go`, `v2_special.go`, `v2_final.go`, `v2_projection.go` и соответствующие `_test.go`; расширить `state.go`/`command.go` совместимыми мостами. Компилятор пакета — `internal/playpack/compile.go`.

**Interfaces:**
- `State.Gameplay *Session`; `View.Gameplay *SessionView`, JSON `gameplay,omitempty`.
- `NewSession(rules Rules) *Session`; `DefaultRules() Rules`; `Rules.Validate() error`.
- `ApplyAt(s State, actorID string, c Command, nowMS int64, entropy uint64) (State,error)`; `Apply` остаётся старым API, вызывает ApplyAt с нулевым временем для v1.
- `ProjectAt(s State, id string, nowMS int64) View`; `Project` совместимый мост.
- `PauseAt(s State, nowMS int64) (State,bool)`; `Pause` совместимый мост. `AdvanceAt(s State, nowMS int64) (State,bool)` — только внутренний тик. `NextDeadline(s State) int64` — 0 если нет активного срока.
- `Command` сохраняет v1-поля; v2 использует `Version=2, ID, Type, ExpectedSeq?, Data json.RawMessage`. Для v2 любые заполненные v1-поля запрещены; `Data` строго декодируется в отдельный тип payload по Type (unknown fields rejected).
- `playpack.Compile(doc,questionID) (game.QuestionSpec,error)` и `playpack.Round(doc,roundID) (game.RoundSpec,error)`; `playpack.Validate` проверяет поддержку v2, отдельно `ValidateLegacy`/`Command` сохраняют v1-профиль для старых комнат.
- Конкретные JSON-поля SessionView и payload фиксируются в `v2_types.go` до старта UI; агент сообщает готовность файла, далее изменения контракта согласуются с root.

- [ ] RED тесты: ordinary wrong→excluded→other wins; noRisk +2price/0; forAll независимые решения; secret получатель/диапазон; auction eligibility/turn/pass/all-in; stakeAll закрытые ставки; final elimination/первый вопрос/positive eligibility. Каждая команда должна сохранять исходный State неизменным при отказе.
- [ ] Реализовать этапы/переходы по Docs14, формальные таймеры, pause/recover, правила/choosing, повторная игра только через явное действие с новым instanceID. `StageID` и `InstanceID` обязательны для зависимых v2-действий; simultaneous wager/answer/buzz не требуют ExpectedSeq.
- [ ] RED→GREEN для приватности каждой роли, замены ведущего, апелляции/коррекции, stale stage, late answer, false start и seeded random window. Проекция содержит разрешённые действия/пределы, только текущие раскрытые фрагменты и последние 20 публичных решений.
- [ ] Компилятор: контекстный default в финале=stakeAll; типы simple/withButton/stake/stakeAll/secret/forAll/noRisk; параметры и NumberSet из SIQ; несколько текстовых фрагментов и ответов; ошибка диагностируется по пути. Медиа пока получает отдельную ошибку подготовки, а не исполняется как текст.
- [ ] `go test -race ./internal/game ./internal/playpack`, передать root точный контракт и RED/GREEN evidence; root коммитит после интеграции.

## Task 2 — Транзакции, HTTP, сроки (root)

Файлы `internal/storage/postgres/{commands,game_setup,store}.go`, новые `game_v2_test.go`; `internal/server/{play,room,connection,server}.go`, новые `game_v2.go`/tests; при необходимости миграция 00007 только для новых данных, не правка применённой схемы.

**Interfaces:** сохранять `Execute`, внедрять серверное `nowMS` и entropy после хеша входного запроса; событие содержит время/энтропию для воспроизведения. `Advance(ctx,roomID)` блокирует строку, fenced ownership, применяет AdvanceAt и сохраняет событие только при изменении. `GameSetup.Rules *game.Rules` создаёт v2; nil остаётся legacy для существующих тестов/комнат. Production createGame всегда задаёт Rules.

HTTP: `POST /rooms/{room}/round` с `{round_id,command_id,expected_seq}`; `/select` прежний контракт + `replay` bool для явного повтора. Сервер получает спецификацию только из pin и выдаёт внутренние begin_round/begin_question. `POST /rooms/{room}/invite` перевыпускает приглашение только ведущему. `GET /rooms/{room}/results` — очки и безопасные решения, постранично; не отдаёт сырые private payload событий.

- [ ] RED: v2 create defaults, direct WS begin_question denied; player race/wager retries, expired callbacks, restart pause, stale invitation, data leaks including result/reconnect.
- [ ] В Execute записывать серверное время/энтропию отдельно от идентичности команды; тик не создаёт дубли receipts. Существующий v1 receipt contract не изменять.
- [ ] Один таймер на actor по NextDeadline, перевзводимый после изменения/attach; нет опроса БД на каждую комнату каждые 250мс. Потеря host/owner приостанавливает все активные стадии. Проверка online-host и detach берёт текущую роль; transfer требует подключённого зрителя.
- [ ] Интегрировать round/select/replay/invite/results, лимиты, выбор по роли; обработка таймеров и private specs недоступна через клиентский WS allowlist.
- [ ] Прогнать realPG race tests + vet/build; обновить native demo на v2 без потери проверки 12 игроков.

## Task 3 — Интерфейс игры (владелец web/** после contractready)

Создать `lib/game-v2.ts`, `lib/GameV2.svelte`, `lib/GameRules.svelte`, `lib/game-v2.test.ts`; изменить `CreateRoom`, `room-client`, страницу rooms и QuestionEditor для P0-типов/параметров. Старые комнаты продолжают прежнюю ветку UI.

**Interfaces:** рендер только `SessionView.allowed_actions` и серверных min/max/targets; `send({v:2,id,type,data})`, вложенные stage_id/instance_id из текущего view. HTTP round/select остаются в room page. Главная кнопка/Space работает только при выданном действии; таймер показывает deadline-server_time, не решает исход.

- [ ] RED тесты DTO/форм: нельзя отправить чужую ставку, просроченный этап или неверный тип action; повтор после reconnect сохраняет ID.
- [ ] Лобби с пресетом/настройками; выбор раунда, chooser и кнопка; презентация/фальстарт; устный или текстовый ответ; аукцион/получатель/цена/ставки; финальные темы, закрытые ответы и раскрытие; оценка/апелляция/коррекция/передача; журнал и результаты. Незавершённая форма не становится новым действием после смены stage.
- [ ] Разрешить создание обычных и специальных текстовых вопросов и финального раунда; редактор параметров показывает только поддержанный профиль, SIQ diagnostics сохраняются.
- [ ] `npm run check`, `npm test`, `npm run build`; root делает браузерный проход и проверку секретности/мобильной ширины.

## Task 4 — Приёмка и интеграция (root)

- [ ] e2e обычный вопрос с неверным ответом и продолжением, скрытые ставки/ответы, стандартный финал, передача ведущего; сохранить воспроизводимые scripts и результаты.
- [ ] Полный realPG race/vet/build + web check/test/build; независимый обзор всей ветки самым способным reviewer, один проход регрессий/исправлений.
- [ ] Обновить Docs11/14 фактическими границами, fast-forward main после green; продолжить media/themes/AI/privacy/load по общему поручению. Цель проекта остаётся активной.

## Зафиксированный JSON-контракт v2

Конверт команды: `{v:2,id,type,expected_seq?,data:{…}}`; старые поля text/answer/value/question_id не смешиваются с data. `game.ProjectAt` возвращает прежние room_id/seq/phase/participants/you и `gameplay` ниже. Фаза v2: lobby/running/paused/finished; v1 сохраняет старые фазы. Старое поле question в v2 не используется.

`SessionView`:
- `version:2, rules_version:number, rules:Rules, chooser_id:string, server_time_ms:number`.
- `stage:{id,kind,instance_id?,deadline_ms?,remaining_ms?,expired:boolean,turn_id?}`. Виды: select_question, select_recipient, select_price, auction, collect_wagers, present_question, buzz_open, buzz_window, collect_answer, collect_answers, judge, reveal, resolved, eliminate_theme, select_final.
- `round?:{id,name,type}`; `question?:{id,instance_id,type,theme,price,text,answer?,content_index,content_total}`. Показываются только текущие доступные фрагменты; secret до назначения получателя скрывает theme/price/text/answer.
- `players:[{id,wager_submitted,answer_submitted,judged,blocked_until_ms?,attempted}]`; `self:{wager?:number,answer?:string}`; `submissions:[{id,answer?,wager?,revealed:boolean,correct?:boolean}]`. Закрытые значения отсутствуют, не маскируются строкой.
- `auction?:{leader_id,amount,all_in,active_ids:string[]}`; `final?:{themes:[{id,name}],eligible_ids:string[],order:string[]}`.
- `decisions:[{id,participant_id,correct,delta,reason,question_id}]`, последние20; `appeals:[{id,judgement_id,participant_id,reason,resolved,resolution?}]`, текущие/последние20.
- `allowed_actions:[{type,minimum?,maximum?,step?,modes?:string[],targets?:string[],requires_seq?:boolean}]`. Кнопки/формы появляются по этому массиву; отсутствие действия означает отсутствие права.

`Rules` JSON: `{preset,selection_seconds,answer_seconds,special_seconds,wager_seconds,final_seconds,penalties,buzzer_mode,window_ms,false_start,false_start_seconds,no_risk_multiplier}`. preset classic/friends/custom; buzzer_mode first/random; no_risk_multiplier фиксирован2 в этом профиле (не произвольная формула).

Data payloads (строгие отдельные Go-структуры; неизвестные поля/неверные формы запрещены):
- begin_round `{round:RoundSpec}`, begin_question `{question:QuestionSpec,replay?:boolean}` — только сервер после HTTP pin resolution.
- set_rules `{rules}`; pause/resume/finish `{}`; extend `{stage_id,seconds}`; restart_stage `{stage_id,reason}`.
- advance/open_buzzer/buzz `{stage_id,instance_id}`; answer `{stage_id,instance_id,text}`; wager/select_price `{stage_id,instance_id,amount}`; bid `{stage_id,instance_id,mode,amount?}` (mode bid/pass/all_in).
- select_recipient `{stage_id,instance_id,target_id}`; eliminate_theme `{stage_id,theme_id}`; reveal `{stage_id,instance_id,target_id?}`; judge `{stage_id,instance_id,target_id,correct}`.
- correct_judgement `{judgement_id,correct,reason}`; adjust_score `{target_id,delta,reason}`; appeal `{judgement_id,reason}`; resolve_appeal `{appeal_id,reason}`; transfer_host `{target_id}`.

`RoundSpec` приватный input: `{id,name,type,themes:[{id,name,question_id}],minimum_price,maximum_price}`; question_id — первый вопрос темы для финала. `QuestionSpec`: `{id,round_id,theme_id,theme,type,price,question:TextStep[],answer:TextStep[],right:string[],selection_mode?,price_range?:{minimum,maximum,step},answer_seconds?}`. `TextStep` `{text,duration_ms?,wait_for_finish?,placement?}`; replic показывается только ведущему. Для v2 профиль принимает текст, включая несколько шагов; image/audio/video пока выдают game_media_pending. Type уже нормализован компилятором, включая контекст финала.

Дополнительные граничные решения: первое chooser — первый игрок по месту; после правильного обычного ответа/аукциона выбирающий обновляется; ведущий может выбрать от его имени. Исправление допустимо для текущего завершённого вопроса до нового begin_question; далее adjust_score с причиной только между вопросами. `restart_stage` не отменяет уже начисленные очки: разрешён только до первого judge текущего вопроса; заново начинает сбор ставок/ответов или текущий выбор, фиксирует новые пределы ставок из текущего счёта. Полная история берётся из событий PostgreSQL; текущий снимок хранит максимум64 последних решений/апелляций и все решения текущего вопроса (до12), проекция выводит20.
