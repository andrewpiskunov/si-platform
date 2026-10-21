# MCP авторские инструменты — спецификация FR-15

Цель: внешний клиент с явно выданным автором токеном может создать частный пакет, прочитать свой черновик, изменить вопросы с защитой от конфликтов, получить диагностику и скачать SIQ. Публикация остаётся действием автора в сайте. Встроенный ИИ использует тот же прикладной слой; MCP не является моделью.

Выбор: официальный Go SDK v1.8.0, Streamable HTTP /api/mcp, stateless JSON responses; версии протокола согласует SDK. Альтернативы: собственный JSON-RPC увеличивает протокольные ошибки; отдельный MCP-сервис дублирует авторизацию/квоты. На первом этапе только уже существующие PAT с ограниченными scopes, сроком 30 дней и отзывом. Это интеграция для клиентов с настраиваемым Bearer header, не реализация OAuth discovery/consent и не обещание совместимости с любым облачным ассистентом. OAuth-подключение требует отдельного этапа; не выдаём PAT endpoint за соответствующий OAuth-спецификации.

## Контракт

- create_draft {idempotency_key, document}: ограниченный packs.Document, без загружаемых файлов/доверенных importer-полей; создаёт только private. Ответ card, без object keys.
- get_draft {pack_id, revision?}: только владелец, текущая либо конкретная ревизия. Возвращает card+document, без внутренних object keys. Существующий предел документа 10MiB, транспортный ответ ограничен 12MiB. При превышении понятная ошибка, пакет доступен в обычном редакторе.
- upsert_questions {pack_id,base_revision,idempotency_key,round_id,theme_id,questions}: 1..100 полных packs.Question. Только существующая тема. По question.id заменить только вопрос в выбранной теме, новые append; не перемещает чужие IDs из других тем. Повторы IDs/неизвестные поля/превышение лимитов запрещены. Другое содержимое и asset metadata сохраняются. Версия обязательна; ошибка инструмента conflict не перезаписывает новую редакцию.
- validate_draft {pack_id,revision?}: диагностика issues/asset_issues/play_issues из существующей валидации. Не утверждает фактическую достоверность вопроса. Возвращает revision и ограниченную первую страницу диагностик с total/truncated.
- export_siq {pack_id,revision?}: проверяет свой материал и scope drafts:export, фиксирует ревизию, возвращает относительный authenticated download_path с revision и MIME. Для загрузки клиент использует тот же Bearer на том же origin. Не помещает архив/токен в prompt, URL query или публичный storage. Настоящее скачивание через существующий bounded disk export, без base64 архива в MCP.

Чтение и диагностика требуют drafts:read; создание/изменение drafts:write; export требует drafts:read+drafts:export. MCP допускает только API credentials, не ambient cookies/session bearer. На каждом HTTP и каждом tool service — live auth до результата/идемпотентного receipt. Другие владельцы, удалённые/blocked материалы недоступны. Никогда не передавать PAT провайдеру.

## Повтор и лимиты

Таблица mcp_receipts: owner_id, idempotency_key (16..128 ASCII без пробелов), operation, request_hash, pack_id, result_revision, created_at; PK owner+key. Хеш canonical parsed input включает operation/pack/base. Account lock→pack lock; receipt вместе с изменением в одной транзакции. Повтор одинакового тела возвращает card сохранённой result_revision, даже если редактор ушёл вперёд; другой hash даёт conflict. Свежая авторизация, ownership и block проверяются прежде receipt. Удалённый target не создаётся заново. Предел 100000 receipts/owner (пакеты 200 + версии) не наращивает память. Receipt независим от пакета (нет FK на material), сохраняется при purge пакета, поэтому поздний retry возвращает not_found и не воскрешает удалённый материал. Метаданные receipt входят в собственную выгрузку аккаунта/пакета; после удаления аккаунта очищаются через 30 дней с учётом export lease. Для живого аккаунта максимум 100000 receipts, после достижения — quota, обычный редактор продолжает работать; лимит описан в инструкции подключения, отказ quota не блокирует обычный редактор.

HTTP body ≤2MiB, вложенность JSON≤64, batches запрещены; response≤12MiB, 16 concurrent requests globally, 30s tool timeout; no long-lived GET/SSE sessions. Response private/no-store/nosniff. Origin отсутствует (CLI) либо точно ALLOWED_ORIGIN; никакого wildcard CORS. Invalid protocol/body handled SDK; errors expose safe sentinel not SQL/internaltext. No fetch URLs, server tools, model calls, prompts/resources exposing privatefiles. SDK tool annotations readOnly/destructive/idempotent are hints; authorization independent.

## Подключение

В аккаунте откройте «Токены доступа → Подключение MCP». Создайте отдельный токен для клиента с нужными разрешениями. Endpoint: `https://<ваш-домен>/api/mcp`; транспорт Streamable HTTP; заголовок `Authorization: Bearer <токен>`. Токен показывается один раз, действует до 30 дней, немедленно отзывается в аккаунте. Подключение через OAuth пока отсутствует. Локальная разработка использует `http://localhost:5173/api/mcp`.

Вызовы возвращают JSON в одном text content MCP: так документ не дублируется в ответе и остаётся читаемым клиентами обеих проверенных версий. SDK согласует версии; проверены `2026-07-28` и `2025-11-25`. Контракт вложенного `packs.Parameter` описан рекурсивным `$ref`; остальные поля схемы выводятся из канонических Go-типов.

Для повторного create/upsert используйте **тот же ключ и то же тело**. После conflict сначала get_draft, затем осознанно сформируйте новую правку с новой базовой версией и новым ключом. Ключ не является токеном доступа. Сохранённые receipts ограничены 100 000 новыми изменяющими операциями на аккаунт; повторы лимит не расходуют. После достижения используйте обычный редактор. Публичной публикации и вызова модели среди инструментов нет.

Пример проверки списка инструментов через curl. Переменные `SI_MCP_URL` и `SI_MCP_TOKEN` задаются локально; токен передаётся curl через stdin конфигурации, а не аргумент командной строки. Не включайте shell tracing. Для production используйте HTTPS.

```sh
curl --config - <<EOF
url = "${SI_MCP_URL}"
request = "POST"
header = "Authorization: Bearer ${SI_MCP_TOKEN}"
header = "Content-Type: application/json"
header = "Accept: application/json, text/event-stream"
header = "MCP-Protocol-Version: 2025-11-25"
data = "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}"
EOF
```

Для экспорта вызовите `export_siq` с `pack_id`, затем скачайте `download_path` на **том же origin**, передав тот же заголовок Bearer. Путь содержит ревизию, но не токен; скачивание заново проверяет права. Ссылка не является публичной и не гарантирует доступ после отзыва токена/удаления материала.

## Приёмка

Real SDK client→httptest/realPG: tool discovery exactly five, create→read→upsert→validate→export path→actual SIQ parse. Negative own/foreign/publicforeign, expired/revoked token before duplicate, missing scopes, cookies-only rejected, badorigin, malformed/oversized/toolinputs. Concurrent identical requests one revision; changedbody/key conflict; concurrent editorCAS no lost update; deleted/blocked receipt not resurrect. Admission/timeout bounded, no outbound URL fetch, raw secrets absent in results/accountZIP. Existing editor and authoring regressions pass. No paid API or external publishing.

Источники: [SDK и версии протокола](https://go.sdk.modelcontextprotocol.io/protocol/), [OAuth для HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization), [транспорт SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/streamable.go). PAT-режим не объявляется OAuth-реализацией.

Статус: реализация и локальная приёмка завершены, независимое ревью этапа ожидается. Полный Go race с PostgreSQL/медиа, vet/build, 103 frontend-теста, Svelte check без ошибок/предупреждений и production build прошли. Официальный SDK проверен по настоящему HTTP для двух версий протокола; SIQ экспорт распакован и проверен. Браузер подтвердил выпуск/отзыв токена, идемпотентное создание, конфликт версии с редактором, частный SIQ и адаптивность без JS-ошибок. Регрессии ИИ и игры на 12 игроков прошли. Реальные облачные клиенты и OAuth не проверены.
