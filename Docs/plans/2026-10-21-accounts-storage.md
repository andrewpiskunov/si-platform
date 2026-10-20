# Аккаунты и объектное хранилище: контракт реализации

**Goal:** подготовить реальные аккаунты авторов и заменяемое потоковое хранилище для загрузок, редактора, каталога и MCP.
**Architecture:** независимые `internal/accounts` и `internal/blob`; HTTP-интеграция пакетов использует оба через контракт ниже. Изменения модулей не затрагивают игровой автомат.
**Tech Stack:** Go, PostgreSQL/pgx, Argon2id, chi, AWS SDK Go v2 S3; локальный файловый адаптер.
**Spec:** Docs/02-REQUIREMENTS.md FR-01/05/15/18/19, NFR-10; Docs/03-ARCHITECTURE.md §6/7.
**Execution:** `dispatching-parallel-agents`: по одному исполнителю на независимый модуль, общий модуль интегрирует основной агент. Файлы, схема и зависимости распределены явно; повторного разрешения на реализацию пользователь не требует.

## Global Constraints

Go 1.27.3. Нет токенов/паролей в логах. Секреты хешируются в БД. Проверки с настоящей PostgreSQL используют изолированные testutil-схемы. Агент аккаунтов владеет только `internal/accounts/**` и `migrations/00003_accounts.sql`; агент файлов — `internal/blob/**`. Только основной агент меняет `go.mod/go.sum`, существующий server/cmd и общую документацию. Не выполнять git commit параллельно; коммиты делает основной агент после проверки.

## Task 1: Аккаунты, сессии и персональные токены

**Files:** `internal/accounts/{service,password,http,service_test,http_test}.go`, `migrations/00003_accounts.sql`.
**Types:** `User{ID, Username, DisplayName, Role string; CreatedAt time.Time}`, `Session{User User; Token string; ExpiresAt time.Time}`, `Identity{User User; Scopes []string; SessionID string; APIToken bool}`, `Options{Origin string; SecureCookies bool}`.
**Service:** `New(pool *pgxpool.Pool, options Options) *Service`; `Register(ctx,username,password,displayName string)(Session,error)`; `Login(ctx,username,password string)(Session,error)`; `Authenticate(ctx,token string)(Identity,error)`; `FromRequest(r *http.Request)(Identity,error)`; `Handler() http.Handler` с относительными маршрутами, монтируется в `/api/account`.

HTTP: POST `/register {username,password,display_name}`; `/login {username,password}`; `/logout`; GET `/me`; POST `/password {current_password,new_password}`; GET `/export`; DELETE `/me {password}`. Ответ register/login — `{user,token,expires_at}` и HttpOnly SameSite=Lax cookie `si_session`, Path=/, срок 30 суток; Secure задаётся для HTTPS. Прочие ответы содержат `{user}` либо `{ok:true}`. Ошибка `{error:code}`. Cookie не меняет серверную проверку срока/отзыва. `FromRequest` принимает cookie или Bearer; неверный Bearer не откатывается к cookie.

Токены MCP: POST `/tokens {name,scopes}` → token (показывается один раз), id, expires_at; GET `/tokens` только метаданные; DELETE `/tokens/{id}` только свои. Срок 30 суток; допустимые scopes: `drafts:read`, `drafts:write`, `drafts:export`, `ai:generate`; публикация/модерация не выдаются. API-токен не управляет аккаунтом/другими токенами. `Identity` позволяет прикладному слою проверить scope. Register по умолчанию role=author. Роли moderator/admin выставляются отдельным административным путём, не клиентским JSON.

Username: ASCII [a-z0-9_], 3–32, регистр нормализуется; display_name 1–64 Unicode; пароль 12–128 символов. Argon2id с солью из crypto/rand: 64 MiB, t=3, p=2, ключ 32 байта; одновременно максимум 4 вычисления, ожидание отменяется context. Вход по несуществующему пользователю проверяет dummy hash. Токен — 32 случайных байта, в БД SHA-256. Password change отзывает все сессии/токены; удаление анонимизирует identity и отзывает доступ. Метаданные опубликованных версий и удаление контента обрабатывает прикладной слой отдельно.

Проверка Origin у браузерных мутаций, JSON ≤16 KiB, неизвестные поля отклоняются. Rate limit регистрации/входа с ограниченным размером внутренней таблицы; одинаковая внешняя ошибка неверного пользователя/пароля. Аудит только ID/действие/время, без секретов. Запросы SQL параметризованы; предусмотреть конкурентное создание одинакового username.

- [ ] RED: регистрация/вход, хеширование, коллизия username, чужой токен, срок, отзыв, смена пароля, удаление, cookie/Origin, ограничение MCP scope/чужого token ID; запуск `go test ./internal/accounts` до реализации.
- [ ] Реализация + GREEN с `TEST_DATABASE_URL=… go test -race ./internal/accounts`; self-review, список выполненных проверок.

## Task 2: Локальное и S3-хранилище

**Files:** `internal/blob/{store,local,s3,store_test,s3_test}.go`.
**Interface:** `Store` с `Put(ctx context.Context,key string,body io.Reader,size int64,contentType string) error`, `Stat(ctx,key)(Metadata,error)`, `Get(ctx,key string,offset,length int64)(io.ReadCloser,error)`, `Delete(ctx,key) error`; `Metadata{Size int64;ContentType,ETag string}`. offset=0,length=-1 означает весь объект; прочие диапазоны проверяются; ошибки `ErrNotFound`, `ErrInvalidKey`, `ErrInvalidRange` проверяются через errors.Is.

`NewLocal(root string)(Store,error)`, `NewS3(ctx context.Context, cfg S3Config)(Store,error)`; `S3Config{Endpoint,Region,Bucket,AccessKey,SecretKey,SessionToken string;PathStyle bool;TempDir string}`. Пустые статические ключи разрешают стандартную цепочку AWS credentials. S3 bucket создаёт оператор, приложение не меняет ACL и не делает объекты публичными.

Key — ровно 64 строчных hex-символа SHA-256 байтов. Максимальный объект 250 MiB. Put сверяет фактические размер и хеш до публикации; ошибочная повторная загрузка не повреждает существующий объект. Одинаковый Put безопасен. Потоковое чтение и ограниченное временное хранение на диске; не буферизовать весь объект в RAM. Локальный адаптер использует безопасные файловые операции и атомарное завершение. Частичные файлы удаляются. Метаданные сохраняются для Stat; GET поддерживает диапазоны без чтения всего файла. Нет публичных presigned URL: проверку прав и HTTP-кеширование обеспечивает будущий API.

- [ ] RED: Put/Get/Stat/Delete, диапазон, неверный key/hash/size, отмена, повтор без повреждения, symlink/path escape; `go test ./internal/blob` до реализации.
- [ ] Реализация + GREEN `go test -race ./internal/blob`; для S3 локальный HTTP test server проверяет фактические запросы/range/errors и отсутствие секрета в ошибках. Настоящая проверка S3 endpoint помечается отдельно.

## Review Focus и интеграция

- Пароль/ключ/токен не попадает в ошибки и аудит; неизвестные поля не повышают роль.
- Отозванный токен перестаёт работать, включая гонку смены пароля/входа.
- Ограниченный токен не даёт доступ к аккаунту, публикации или чужому черновику.
- Фальшивый размер/хеш объекта и отмена не повреждают прежние данные.
- Range не читает больше указанного и не допускает отрицательные/переполняющие значения.

- [ ] Основной агент проверяет diff обоих модулей, общий `go test -race ./...`, `go vet`, новые миграции и фиксирует коммиты.
- [ ] Прикладной HTTP-этап подключает аккаунты и файлы к приватным черновикам, ревизиям, каталогу и комнате; отдельный интеграционный план.
