# Редактор и поиск — план реализации

> **For agentic workers:** superpowers:executing-plans для ledger/TDD/общей приёмки; superpowers:dispatching-parallel-agents для независимых модулей. Один свежий обзор всего этапа после интеграции, одна коррекция, без обзоров каждой задачи.

**Goal:** завершить перестановку структуры и поиск/возрастной фильтр из FR-04/07.
**Architecture:** существующий DraftEditor принимает переставленный документ; immutable library revisions получают поисковые метаданные; каталог фильтрует только актуальную опубликованную ревизию.
**Tech Stack:** существующие Go/pgx/PostgreSQL 18, pg_trgm, SvelteKit/TypeScript, Chromium.
**Spec:** [Docs/17-AUTHORING-COMPLETION.md](../17-AUTHORING-COMPLETION.md).

## Global Constraints

- Перемещение только внутри родителя; ID/параметры/неизвестные поля/выбор сохраняются; одна операция — одна отмена и обычное автосохранение.
- Поиск буквальной подстроки без регистра: название, автор, темы; ответы и приватные ревизии исключены.
- Age string `""|unrated|0|6|12|16|18`; число — максимум включительно; распознаны только точные метки с `+` после trim `space/\t/\r/\n`, остальные `NULL`.
- Производные колонки не меняют Card/Document/SIQ. search_text COLLATE pg_catalog.pg_unicode_fast (PG18 UTF-8), pg_trgm в public, миграционная блокировка; откат не удаляет общее расширение.
- Нет новых аккаунтов/сервисов/разрешений. Существующие фильтры, права публикации, тема комнаты и конфликты сохраняются.

## Review Focus

1. Пустые диагностические массивы и расширения должны пережить перестановку/миграцию — Task1/2.
2. Выбранный вопрос после перемещения его родителей и отмены остаётся тем же — Task2/3.
3. Непубличная новая ревизия и закрытые ответы не должны находиться по поиску — Task1.
4. `%`, `_`, `\`, кириллица и неизвестные возрастные строки должны иметь буквальный предсказуемый смысл — Task1/3.
5. Перезагрузка, пагинация и переход с `?room=` сохраняют фильтр и оформление, но не передают приватные данные — Task3.

## Task 1 — Индекс и сервер каталога (root)

**Files:** `migrations/00010_catalogue_search.sql`, `internal/library/catalogue_metadata.go` и tests, `internal/library/{model,service,publication}.go`, `internal/server/authoring.go` и tests.
**Interfaces:** `Filter.Age string`; внутренний `catalogueMetadata(card Card, document packs.Document) (searchText string, ageRating *int)`; INSERT добавляет два параметра. HTTP принимает `age`; DTO неизменны.

- [x] RED `TestCatalogueThemeSearchAndAge`: опубликованные темы/регистр/буквальные спецсимволы, все возрастные варианты/границы/комбинации, private/pending/newdraft/withdraw/block исключены; ответы не найдены. `TestCatalogueMetadataBackfill`: данные до migration10 (включая null rounds/themes), затем миграция, сравнение с новым сохранением; повтор миграции и параллельные изолированные схемы безопасны.
- [x] Run `. media-env.sh; TEST_DATABASE_URL=... go test -race ./internal/library -run 'TestCatalogue(ThemeSearchAndAge|MetadataBackfill)'`; ожидаемый RED — отсутствующий Age/метаданные или неверный поиск.
- [x] Добавить чистое вычисление метаданных, миграцию с backfill/индексами/constraint age, заполнение INSERT, строгую проверку фильтра и параметризованный буквальный ILIKE. Все варианты создания/восстановления используют общий insert.
- [x] RED→GREEN HTTP-тест invalid age/recognized/unrated + существующие фильтры; `go test -race ./internal/library ./internal/server ./internal/storage/postgres` и vet GREEN. Scoped commit.

## Task 2 — Перестановка в редакторе (независимый web worker)

**Files:** новые `web/src/lib/reorder.ts`/tests и `ReorderControls.svelte`; существующие `QuestionEditor.svelte`, `routes/studio/[id]/+page.svelte`, при необходимости локальные styles. Не менять каталог/e2e/package.json.
**Interfaces:** `moveItem<T>(items: readonly T[], from: number, to: number): T[]` возвращает новый массив, не меняет элементы; некорректные/равные индексы возвращают исходный порядок. `ReorderControls` props `{label:string,index:number,count:number,onmove:(delta:-1|1)=>void}`. Точные aria-label: `${label}: выше` / `${label}: ниже`.

- [x] RED unit: move parents/question/content preserves all IDs/unknown metadata/refs; no mutation on bad/boundary inputs; DraftEditor undo restores prior document and next save holds reordered document despite stale prior save completing.
- [x] `node --test src/lib/reorder.test.ts` → отсутствующий helper RED. Реализовать helper и кнопки; все действия через существующий `change`/QuestionEditor onchange, операции границ disabled, выбранный ID не заменяется индексом. Сохранить null-нормализацию diagnostic drafts.
- [x] UI уровней: раунд, тема, выбранный вопрос в своей теме, content group question/answer. Кнопки клавиатурные, подписи с именем/номером и направлением; компактны на 390px.
- [x] `npm test`, `npm run check`, `npm run build` → GREEN; один scoped commit после интеграции root, без собственного reviewer.

## Task 3 — Фильтр и браузерная приёмка (root после Task1/2)

**Files:** `web/src/routes/+page.{server.ts,svelte}`, новый `e2e/authoring.mjs`, `e2e/package.json`, README/Docs11/17/план.
**Consumes:** Task1 `age` query, неизменный Card; Task2 кнопки с exact aria-label. API/Vite перезапускает только root после стабильных исходников.

- [x] RED Chromium: поиск по теме + age через форму/SSR; pagination preserves query/age/context; invalid API age fails. Показать подсказку «Указано автором; фильтр не проверяет содержание пакета» и все семь вариантов фильтра.
- [x] Implement SSR allowlist/form and preservation; стандартные фильтры остаются совместимы.
- [x] RED→GREEN Chromium reordered rounds/themes/questions/question+answer fragments → selected question stable → undo → autosave/reload → exported SIQ/reimport same order. Diagnostic unknown fields/refs retained; mobile overflow absent; keyboard controls work; no page errors. Создавать синтетические пакеты только локально.
- [x] Полный Go race с PostgreSQL и MEDIA_INTEGRATION, vet/build; web tests/check/build; существующие шесть e2e (включая theme-regressions) + authoring. Результаты записать, не выдавая их за нагрузочное испытание.
- [x] Один свежий обзор всего этапа с Review Focus; Important/Critical — одна проверяемая коррекция. Обновить фактический статус и scoped commits, FF локального main; продолжить остальные P0.
