# Game Core Implementation Plan

> **For agentic workers:** use `superpowers:executing-plans` for this implementation. The user has requested implementation of the next step; execute in this session.

**Goal:** запускаемый Go-прототип комнаты: ведущий, до 12 игроков, кнопка, решение, PostgreSQL и восстановление.

**Architecture:** последовательный обработчик на комнату, чистые переходы состояния и транзакционная запись снимка, события и результата команды. Клиент получает только разрешённую проекцию после commit. В этой итерации один процесс владеет БД прототипа; отдельная advisory-lock сессия защищает от второго процесса, потеря этой сессии останавливает обслуживание.

**Tech Stack:** Go, chi, coder/websocket, pgx, sqlc, goose, PostgreSQL.

**Spec:** [требования](02-REQUIREMENTS.md), [архитектура](03-ARCHITECTURE.md), [ближайший шаг](04-ROADMAP.md).

## Global Constraints

- До 12 игроков, 1 ведущий и 20 зрителей; роли выводятся из сохранённой сессии.
- Входящий WS-кадр ≤16 KiB; исходящая очередь ≤64 KiB; 10 команд/с с запасом на всплеск 20.
- Подтверждение после commit; ID команды привязан к участнику, повтор с другим содержимым отклоняется.
- При восстановлении отправляется актуальный снимок с последовательностью; дедупликация сохраняется после перезапуска.
- Ответ видит ведущий; игрокам он раскрывается после решения. Только ведущий открывает вопрос и начисляет очки.
- Первый прототип использует текстовые вопросы, переданные ведущим через API. Импорт SIQ, специальные вопросы, полный UI, AI, каталог, темы и кластеризация остаются следующими этапами.
- Это локальный технический прототип, не подтверждение NFR на 10 000 соединений. По умолчанию слушает 127.0.0.1.

## Review Focus

- Повтор команды с тем же ID и другими полями: конфликт без изменения счёта.
- Сбой БД и неопределённый результат commit: отсутствие положительного подтверждения, повтор безопасен.
- Конкурирующие нажатия и старый question_id: один победитель, старый вопрос не меняется.
- Чужой токен, подмена роли и Origin: доступ отклоняется, ответ не утекает.
- Медленный клиент, отсутствие ведущего, остановка процесса: ограниченные очереди и восстановление с паузой.

## Интерфейсы и поведение

HTTP: `POST /api/rooms {host_name}` → room_id, participant_id, token, invite_token.
`POST /api/rooms/{id}/participants {name, role, invite_token}` → participant_id, token.
`GET /healthz` — живость; `GET /readyz` — БД и владение процессом.

WS: `/api/rooms/{id}/ws`; первым кадром за 5 секунд отправляется `{v:1,type:"authenticate",token}`. Секреты не передаются в URL. Затем сервер отправляет снимок. `snapshot` содержит `seq`, фазу, игроков, вопрос без скрытого ответа и победителя.

Команды: `{v:1,id,type,question_id,text,answer,value,correct}`. Типы: `open_question`, `buzz`, `judge`, `pause`, `resume`, `finish`. Поля применяются к своему типу; неизвестные поля/типы и лишние значения отклоняются. Фазы: lobby → open → answering → resolved; pause сохраняет предыдущую фазу; finish завершает комнату. `open_question` допустим из lobby/resolved при двух игроках. В простом режиме одна попытка на вопрос: верный ответ даёт цену, неверный вычитает её; следующее открытие создаёт новый question_id.

`result` содержит ID команды, код, номер её результата, duplicate и текущий снимок. При повторе сохраняется номер первоначального результата, снимок может быть новее. Ошибки протокола не меняют игру. Разрыв последнего соединения ведущего ставит активный вопрос на паузу; после перезапуска активные вопросы тоже приостанавливаются.

## Task 1: Переходы состояния

**Files:** `go.mod`, `internal/game/{state,command}.go`, `internal/game/game_test.go`.

**Interfaces:** `game.Apply(State, actorID string, Command) (State, error)`; `game.Project(State, participantID string) View`; `game.AddParticipant(State, Participant) (State,error)`. State хранит участников, фазу, текущий вопрос, использованные ID и sequence; View не раскрывает секретов.

- [ ] Написать тесты `TestRound`, `TestRolesAndStaleQuestion`, `TestParticipantLimits`, `TestProjection`, `TestPauseResume`, `TestCommandValidation`: +100/-100, один победитель, запрет 13-го игрока и 21-го зрителя, ответ скрыт, исходное состояние не изменяется при ошибке.
- [ ] `go test ./internal/game` — увидеть отсутствие реализации / FAIL.
- [ ] Реализовать переходы и валидацию без сети и БД.
- [ ] `go test ./internal/game` — PASS; сохранить коммит.

## Task 2: Долговечность и идентичность

**Files:** `internal/storage/postgres/{store,commands,migrate}.go`, `migrations/00001_core.sql`, `sql/queries.sql`, `sqlc.yaml`, `internal/storage/db/*`, `internal/storage/postgres/store_test.go`, `internal/testutil/database.go`.

**Interfaces:** `Store.Create(ctx,hostName) (Credentials,error)`, `Store.Join(ctx,roomID,invite,name,role) (Credentials,error)`, `Store.Authenticate(ctx,roomID,token) (string,error)`, `Store.Load(ctx,roomID) (game.State,error)`, `Store.Execute(ctx,roomID,actorID,cmd) (Outcome,error)`, `Store.Pause(ctx,roomID,reason)`, `Store.Recover(ctx)`. Outcome содержит state, code, result sequence и duplicate.

- [ ] Написать PostgreSQL-тесты: повтор после нового Store не удваивает +100; изменённый повтор конфликтует; 12 конкурентных buzz дают одного победителя; чужой токен не подходит; конкурентный вход не превышает 12; сбой транзакции не оставляет половину решения.
- [ ] `TEST_DATABASE_URL=… go test ./internal/storage/postgres` — FAIL до реализации.
- [ ] Добавить SQL/миграцию, сгенерировать sqlc; транзакция блокирует строку комнаты, проверяет ID, применяет переход, сохраняет snapshot/event/result.
- [ ] Проверить восстановление, паузу и хеширование токенов. Повторить тесты — PASS; сохранить коммит.

## Task 3: WebSocket, запуск и демонстрация

**Files:** `internal/server/{server,room,connection}.go`, `internal/server/server_test.go`, `internal/storage/postgres/lease.go`, `cmd/{server,migrate,demo}/main.go`, `compose.yaml`, `.env.example`, `Makefile`, `README.md`, `Docs/10-GAME-CORE.md`.

**Interfaces:** `server.New(store,*options) *Server`, `Server.Handler() http.Handler`, `Server.Close()`; сериализация команд/подписок через bounded channel комнаты. Сессия владения БД живёт отдельно от пула запросов.

- [ ] Написать реальные HTTP/WS-тесты: 12 игроков, ответы по ролям, race buzz, judge/retry, reconnect, рестарт сервера с той же БД, Origin/auth, переполнение очереди и пауза при уходе ведущего.
- [ ] `TEST_DATABASE_URL=… go test ./internal/server` — FAIL до реализации.
- [ ] Реализовать маршруты, auth frame, heartbeat, лимиты, ограниченные очереди и остановку; добавить CLI, миграции, демонстрационный клиент и команды запуска.
- [ ] Прогнать PostgreSQL-интеграцию, `go test -race ./...`, `go vet ./...`, сборку, демонстрацию и проверку зависимостей. Исправить обнаруженные ошибки.
- [ ] Обновить документацию фактически выполненным объёмом; сохранить коммит, провести отдельное ревью и перенести готовый результат в основную рабочую папку.
