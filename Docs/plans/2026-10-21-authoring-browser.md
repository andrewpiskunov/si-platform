# Авторский API и первый браузерный проход

**Goal:** связать аккаунт → SIQ/редактор → сохранённую версию → каталог/модерацию → комнату с серверным табло. Это этап полного MVP, не замена оставшихся спецправил/ИИ/своих тем.
**Architecture:** `internal/library` хранит авторство, неизменяемые ревизии, публикации и жалобы; `internal/server` соединяет библиотеку с accounts/blob/siq/игрой; SvelteKit в `web/` вызывает Go API с той же origin. Ответы и приватные файлы не входят в публичные карточки.
**Execution:** независимые исполнители библиотеки и `web/`, основной агент владеет HTTP/загрузками/комнатой. Разрешение на полную реализацию уже дано. Исполнители не меняют общие файлы и индекс Git.

## 1. Библиотека (исполнитель; только internal/library/**, migrations/00004_library.sql)

`New(pool *pgxpool.Pool) *Service`. Все публичные методы принимают `ctx context.Context`; авторизованные также `actor accounts.Identity`. API-токены требуют соответствующего scope, публикация/модерация только session. Ошибки `ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict, ErrInvalid, ErrBlocked, ErrQuota` проверяются errors.Is.

Модель JSON:
- `Asset{Name,Key,ContentType string; Size int64}` (name — имя файла SIQ, key — SHA256; никакого URL).
- `Rights{License,SourceURL,Attribution string; AllowDownload,Confirmed bool}`; snake_case JSON. Источник — http(s) URL либо `original` для своего произведения; лицензия/основание и атрибуция обязательны.
- `Card{ID,Name,OwnerID,Author,Status,Language,Restriction string; Revision,Difficulty,QuestionCount,RoundCount int; SizeBytes int64; HasMedia,AllowDownload bool; Tags []string; Rights Rights; UpdatedAt time.Time}`. snake_case. Public card содержит только опубликованную ревизию. Status=draft/pending/published/rejected/withdrawn/blocked.
- `Draft{Card Card; Document packs.Document; Assets []Asset; OriginalKey string; Issues []packs.Issue}`. snake_case. Assets/OriginalKey приватны. Issues вычисляет библиотека из Document + дополнительных Issues, полученных от доверенного импортёра. Клиент не задаёт дополнительные Issues напрямую.
- `Save{Document packs.Document; Assets []Asset; OriginalKey string; Issues []packs.Issue}`. Вход доверенного прикладного слоя, не HTTP DTO.
- `Filter{Query,Language,Tag string; Difficulty *int; HasMedia *bool; MaxBytes int64; Offset,Limit int}`; max limit50, offset max100000.
- `Report{ID,PackID,ReporterID,Reason,Status string; Revision int; CreatedAt time.Time}`.

Методы (в скобках результат):
`Create(ctx,actor, Save)(Draft,error)`; `Update(ctx,actor,id string,base int,Save)(Draft,error)`; `Get(ctx,actor,id string,revision int)(Draft,error)` (0=current); `Mine(ctx,actor)([]Card,error)`; `History(ctx,actor,id)([]Card,error)`; `Delete(ctx,actor,id)error` (soft delete + снять публикацию).
`Submit(ctx,actor,id string,revision int,rights Rights)(Card,error)`; `Withdraw(ctx,actor,id)error`; `Catalogue(ctx,filter)([]Card,error)`; `Public(ctx,id)(Card,error)`; `ForPlay(ctx,actor,id string,revision int)(Draft,error)` (owner revision или текущая опубликованная, блокировка запрещает всех); `ForDownload(ctx,actor,id string,revision int)(Draft,error)` (владелец либо публичная разрешённая; пустой actor допустим публично).
`Pinned(ctx,id string,revision int)(Draft,error)` — только доверенному серверу, без клиентского endpoint: неизменяемая уже закреплённая версия, блокировка/удаление немедленно отказывает; снятие публикации не ломает начатую игру.
`Queue(ctx,actor)([]Card,error)`; `Decide(ctx,actor,id string,revision int,decision,reason string)(Card,error)` decision=approve/reject/block, роль moderator/admin; блокировка возможна любой текущей публикации. Сохранять журнал решений.
`Report(ctx,actor,id string,revision int,reason string)(Report,error)`; `Reports(ctx,actor)([]Report,error)`.

Сохранение черновика допускает диагностируемые ошибки; публикация/игра/экспорт требуют отсутствие error. Публикация закрепляет ревизию; последующее редактирование не меняет её. Update с устаревшей base возвращает Conflict, без потери данных. Все мутации транзакционные; lock пользователя (проверить deleted_at), затем pack. Не более 200 активных пакетов автора/500 ревизий на пакет; документ ≤10MiB, assets ≤5000/1GiB; 10GiB логического хранения версий на автора. Проверить asset keys/size/name и отсутствие алиасов; вопросы/медиа по существующему валидатору. Миграция использует FK account_users(id). Удалённые аккаунты не публикуются/не создают данные; DELETE аккаунта прикладной слой снимет собственные публикации.

Тесты RED→GREEN с настоящей PG: чужие пакеты/токены/срок, конфликт двух update, неизменяемая опубликованная ревизия, модераторский переход и блокировка, права на скачивание, фильтры и отсутствие ответов в карточках, жалобы, лимиты, удаление, rollback. Затем race/vet/self-review. Не менять go.mod/go.sum/server/web/docs, не коммитить.

## 2. HTTP и связка игры (основной агент)

Все endpoint `/api`, JSON snake_case; ошибки `{error:code}`. Cookie accounts уже готов. Мутации проверяют Origin и Content-Type, тела ограничены. Публичная выдача файлов не основана на знании хеша. Ответы версии доступны только автору/модератору; игра получает лишь текущий разрешённый шаг.

- GET `/catalogue?q=&language=&tag=&difficulty=&media=&max_bytes=&offset=` → `{items:Card[]}`; GET `/catalogue/{id}` → Card; GET `/catalogue/{id}/download` → SIQ только при разрешении.
- GET `/drafts` → `{items:Card[]}`; POST `/drafts {document}` → Draft, 201 (допустимо `{}` — новый пустой шаблон); GET `/drafts/{id}?revision=` → Draft; PUT `/drafts/{id} {base_revision,document}` → Draft; GET `/drafts/{id}/history` → `{items:Card[]}`; DELETE → `{ok:true}`.
- GET `/drafts/{id}/export`, `/original`; GET `/drafts/{id}/assets/{key}` (для автора); POST `/drafts/{id}/publish {revision,rights}` → Card; POST `/drafts/{id}/withdraw` → `{ok:true}`.
- POST `/uploads {name,size}` → `{id,offset:0,size,status:"uploading"}`; GET `/uploads/{id}` → `{id,offset,size,status,draft_id?,error?}`; PATCH `/uploads/{id}` с `Upload-Offset` и binary ≤4MiB → JSON текущего offset. Offset обязан совпасть, повтор завершённого диапазона не дописывается. POST `/uploads/{id}/complete` без тела → 202 status queued. Фоновый ограниченный worker импортирует в приватный draft; UI опрашивает до ready/failed. 2 активных загрузки на автора/16 всего, срок 24ч, размер ≤250MiB; оригинал сохраняется. Завершение повторяемо. Перезапуск восстанавливает queued/processing.
- GET `/moderation` → `{items:Card[]}`; POST `/moderation/{id} {revision,decision,reason}` → Card; GET `/reports` → `{items:Report[]}`; POST `/catalogue/{id}/reports {revision,reason}` → Report.
- POST `/rooms {host_name,pack_id,revision,title,theme}` → существующие Credentials. Создание требует session; pack_id пустой использует собственный demo для первого знакомства. theme classic/pirate/anime. Игроки/зрители по-прежнему входят существующим POST participants.
- GET `/rooms/{id}/board` c Bearer **room token** → `{title,theme,pack_name,rounds:[{id,name,type,themes:[{id,name,questions:[{id,value,played}]}]}]}`. Ответов/текста вопросов нет. POST `/rooms/{id}/select {question_id,command_id,expected_seq}` c room token — ведущий выбирает вопрос из закреплённой версии; сервер подаёт существующую команду open. Возвращает `{ok:true}`; фактическое состояние приходит WS. Существующие WS judge/buzz/pause/resume/finish сохраняются. Позже спецправила расширят этот контракт.

Интеграционные тесты: регистрация → импорт/редактирование → модерация → создание комнаты → выбор вопроса → игрок buzz/judge; владелец/чужой/token scopes; Range/ETag; отказ после блокировки; upload resume/collision/restart; разрыв и повтор. Реальные PG и blob local, тестовые SIQ свои.

## 3. SvelteKit (исполнитель; только web/**)

Svelte 5 + Kit + TypeScript + adapter-node. SSR публичного каталога; Go URL задаётся серверным `API_ORIGIN` (default http://127.0.0.1:8080), Vite proxy /api,/healthz,/readyz включая WS. В production same-origin reverse proxy. Никакого секретного Go URL/ключа в browser bundle. Зависимости через npm с lockfile; проверить актуальные официальные docs. Русский интерфейс, нейтральное название «Своя игра», без копирования чужих изображений.

Маршруты `/` (каталог, поиск/фильтры, пустое состояние), `/login` (вход/регистрация), `/studio` (мои пакеты/создать/загрузка с прогрессом), `/studio/[id]` (визуальный редактор раундов/тем/вопросов и ответов, автосохранение с base_revision, конфликт без потери локального, история/undo/preview, ручная публикация с правами, SIQ export), `/packs/[id]` (карточка/создать комнату/скачать/жалоба), `/rooms/[id]` (вход по invite из URL **fragment**; role player/spectator, затем WS; табло/ведущий/кнопка/очки/пауза/результаты), `/account` (пароль/выход/экспорт/удаление/токены), `/moderation` (queue+reports, решения). Credentials комнаты хранить sessionStorage по roomID; приглашение хранить только fragment и не отправлять в реферере. Секрет пользователя — HttpOnly cookie, токен ответа login не сохранять.

Визуальное направление: игровое вечернее табло с тёмно-синим фоном, ясной типографикой, тёплым акцентом и крупной кнопкой. Карточки с понятными метаданными, редактор — плотная рабочая область. Минимум декора, клавиатурная навигация, aria live, focus-visible, контраст и reduced-motion. Три готовых темы через CSS variables на оболочке комнаты, распространяются на все элементы внутри игровой сессии. CSS/SVG собственные, внешние картинки не нужны. Быстрый игровой маршрут; редактор отдельно.

Сначала meaningful tests протокола WS (повторы command_id, переподключение, устаревший seq) и сохранения черновика (конфликт/защита dirty state), затем реализация. npm check/test/build. Browser QA корневой агент делает на интегрированном стенде; не имитировать успешные API локальными фиктивными данными. Не менять backend/docs/общий gitindex. Полные игровые правила/ИИ/custom themes добавятся после первого прохода, это явно отражать лишь в документации, не техничными заглушками в основном UX.

## Приёмка этапа

- [ ] Library tests/race/vet и self-review.
- [ ] HTTP integration tests/race/vet и migration.
- [ ] web check/test/build и браузерный проход автора/ведущего/игрока.
- [ ] Независимое ревью, один проход исправлений с регрессиями; общая проверка и перенос этапа в main.
