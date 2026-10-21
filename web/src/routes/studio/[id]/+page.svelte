<script lang="ts">
  import AIAuthoring from '#lib/AIAuthoring.svelte';
  import { appendAITheme } from '#lib/ai-model.ts';
  import UsagePanel from '#lib/UsagePanel.svelte';
  import { onMount } from 'svelte';
  import { beforeNavigate, goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, APIError, errorText, statusText } from '#lib/api.ts';
  import { DraftEditor } from '#lib/draft-editor.ts';
  import { normalizeDraft, canWithdraw, canPlayDraft } from '#lib/draft-model.ts';
  import QuestionEditor from '#lib/QuestionEditor.svelte';
  import ReorderControls from '#lib/ReorderControls.svelte';
  import { moveItem, readQuestionSelection, saveQuestionSelection } from '#lib/reorder.ts';
  import QuestionPreview from '#lib/QuestionPreview.svelte';
  import ModerationHistory from '#lib/ModerationHistory.svelte';
  import { previewKey } from '#lib/moderation-model.ts';
  import MediaUpload from '#lib/MediaUpload.svelte';
  import MediaLibrary from '#lib/MediaLibrary.svelte';
  import CreateRoom from '#lib/CreateRoom.svelte';
  import type { Draft, PackDocument, Question, Card, Rights } from '#lib/types.ts';
  let draft = $state<Draft | null>(null),
    document = $state<PackDocument | null>(null),
    loading = $state(true),
    error = $state(''),
    saveLabel = $state(''),
    dirty = $state(false),
    saving = $state(false),
    conflict = $state(false),
    canUndo = $state(false),
    revision = $state(0),
    selected = $state(''),
    preview = $state(false),
    history = $state<Card[]>([]),
    busy = $state(false),
    published = $state(''),
    deleteConfirmed = $state(false),
    rights = $state<Rights>({
      license: '',
      source_url: 'original',
      attribution: '',
      allow_download: false,
      confirmed: false
    });
  let editor: DraftEditor<PackDocument> | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let usageRefresh = $state(0);
  let alive = true;
  let saveTask: Promise<void> | null = null;
  let loadedID = '';
  const questions = $derived(
    document?.rounds.flatMap((round) => round.themes.flatMap((theme) => theme.questions)) || []
  );
  const selectedQuestion = $derived(questions.find((question) => question.id === selected));
  const selectedLocation = $derived(
    document?.rounds.flatMap((round) =>
      round.themes
        .filter((theme) => theme.questions.some((question) => question.id === selected))
        .map((theme) => `${round.name} / ${theme.name}`)
    )[0] || ''
  );
  const selectedPosition = $derived.by(() => {
    for (const [ri, round] of (document?.rounds || []).entries()) {
      for (const [ti, theme] of round.themes.entries()) {
        const qi = theme.questions.findIndex((question) => question.id === selected);
        if (qi >= 0) return { ri, ti, qi, theme };
      }
    }
    return null;
  });
  const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
  onMount(() => {
    loadedID = page.params.id || '';
    void load();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  });
  $effect(() => {
    if (loading || !document) return;
    if (!questions.some((question) => question.id === selected)) selected = questions[0]?.id || '';
    saveQuestionSelection(loadedID, selected);
  });
  beforeNavigate(({ cancel }) => {
    if (dirty && !window.confirm('Есть несохранённые изменения. Покинуть редактор?')) cancel();
  });
  function sync() {
    if (!editor) return;
    document = copy(editor.document);
    revision = editor.revision;
    dirty = editor.dirty;
    saving = editor.saving;
    conflict = editor.conflict;
    canUndo = editor.canUndo;
  }
  async function load() {
    try {
      draft = normalizeDraft(await api<Draft>(`/drafts/${loadedID}`));
      editor = new DraftEditor(draft.document, draft.card.revision);
      rights = { ...rights, ...draft.card.rights, confirmed: false };
      sync();
      selected = readQuestionSelection(loadedID, questions);
      await loadHistory();
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  async function loadHistory() {
    try {
      history = (await api<{ items: Card[] | null }>(`/drafts/${loadedID}/history`)).items || [];
    } catch (e) {
      error = errorText(e);
    }
  }
  function schedule() {
    clearTimeout(timer);
    if (!conflict) timer = setTimeout(() => void save(), 1200);
  }
  function change(mutate: (next: PackDocument) => void) {
    if (!editor || !document) return;
    const next = copy(document);
    mutate(next);
    editor.edit(next);
    published = '';
    sync();
    saveLabel = 'Есть изменения';
    schedule();
  }
  function save(): Promise<void> {
    if (saveTask) return saveTask;
    saveTask = performSave().finally(() => {
      saveTask = null;
    });
    return saveTask;
  }
  async function performSave() {
    if (!editor) return;
    const request = editor.beginSave();
    if (!request) return;
    sync();
    saveLabel = 'Сохраняем…';
    error = '';
    try {
      const result = normalizeDraft(
        await api<Draft>(`/drafts/${loadedID}`, 'PUT', {
          base_revision: request.base_revision,
          document: request.document
        })
      );
      editor.saved(request, result.document, result.card.revision);
      draft = result;
      saveLabel = 'Все изменения сохранены';
      if (alive) {
        usageRefresh++;
        sync();
        if (editor.dirty) schedule();
        void loadHistory();
      }
    } catch (e) {
      editor.failed(e instanceof APIError ? e.code : 'unavailable');
      sync();
      saveLabel = 'Не сохранено';
      error = errorText(e);
    }
  }
  async function flushDraft() {
    clearTimeout(timer);
    if (saveTask) await saveTask;
    if (!editor) throw new Error('unavailable');
    if (editor.conflict) throw new Error('conflict');
    while (editor.dirty) {
      await save();
      if (editor.conflict) throw new Error('conflict');
      if (saveLabel === 'Не сохранено') throw new Error('save_required');
    }
    clearTimeout(timer);
    if (editor.saving) throw new Error('save_required');
  }
  async function attachMedia(uploadID: string, signal: AbortSignal) {
    await flushDraft();
    if (signal.aborted || !editor) return;
    const current = editor,
      request = current.beginAttachment();
    if (!request) throw new Error('conflict');
    sync();
    saveLabel = 'Прикрепляем медиа…';
    try {
      const result = normalizeDraft(
        await api<Draft>(
          `/drafts/${loadedID}/assets`,
          'POST',
          { upload_id: uploadID, base_revision: request.base_revision },
          undefined,
          signal
        )
      );
      current.saved(request, result.document, result.card.revision);
      if (alive && current === editor) {
        usageRefresh++;
        draft = result;
        sync();
        saveLabel = current.dirty ? 'Есть изменения' : 'Все изменения сохранены';
        if (current.dirty) schedule();
        void loadHistory();
      }
    } catch (e) {
      current.failed(e instanceof APIError ? e.code : 'unavailable');
      if (alive) {
        sync();
        saveLabel = 'Не сохранено';
      }
      throw e;
    }
  }
  function undo() {
    editor?.undo();
    sync();
    schedule();
  }
  function newQuestion(final = false): Question {
    return {
      id: crypto.randomUUID(),
      price: 100,
      type: final ? 'stakeAll' : 'simple',
      info: {},
      params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: '' }] }],
      right: ['']
    };
  }
  function addRound(type = 'standard') {
    const q = newQuestion(type === 'final');
    change((next) =>
      next.rounds.push({
        id: crypto.randomUUID(),
        name: `Раунд ${next.rounds.length + 1}`,
        type,
        info: {},
        themes: [{ id: crypto.randomUUID(), name: 'Новая тема', info: {}, questions: [q] }]
      })
    );
    selected = q.id;
  }
  function addTheme(roundIndex: number) {
    const q = newQuestion(document?.rounds[roundIndex].type === 'final');
    change((next) =>
      next.rounds[roundIndex].themes.push({
        id: crypto.randomUUID(),
        name: 'Новая тема',
        info: {},
        questions: [q]
      })
    );
    selected = q.id;
  }
  function addQuestion(ri: number, ti: number) {
    const q = newQuestion(document?.rounds[ri].type === 'final');
    q.price = ((document?.rounds[ri].themes[ti].questions.length || 0) + 1) * 100;
    change((next) => next.rounds[ri].themes[ti].questions.push(q));
    selected = q.id;
  }
  function updateQuestion(value: Question) {
    change((next) => {
      for (const round of next.rounds)
        for (const theme of round.themes) {
          const i = theme.questions.findIndex((question) => question.id === value.id);
          if (i >= 0) theme.questions[i] = value;
        }
    });
  }
  function moveQuestion(delta: -1 | 1) {
    if (!selectedPosition) return;
    const { ri, ti, qi, theme } = selectedPosition;
    if (qi + delta < 0 || qi + delta >= theme.questions.length) return;
    change((next) => {
      next.rounds[ri].themes[ti].questions = moveItem(
        next.rounds[ri].themes[ti].questions,
        qi,
        qi + delta
      );
    });
  }
  function deleteQuestion() {
    change((next) => {
      for (const round of next.rounds)
        for (const theme of round.themes)
          theme.questions = theme.questions.filter((question) => question.id !== selected);
    });
    selected = '';
  }
  async function restore(version: number) {
    busy = true;
    try {
      const prior = normalizeDraft(await api<Draft>(`/drafts/${loadedID}?revision=${version}`));
      editor?.edit(prior.document);
      sync();
      schedule();
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function resolveConflict(keep: boolean) {
    busy = true;
    try {
      const latest = normalizeDraft(await api<Draft>(`/drafts/${loadedID}`));
      if (keep) {
        editor?.rebase(latest.card.revision);
        sync();
        await save();
      } else {
        if (!window.confirm('Заменить локальные изменения сохранённой версией?')) return;
        draft = latest;
        editor = new DraftEditor(latest.document, latest.card.revision);
        sync();
        error = '';
      }
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function publish(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      await save();
      if (!editor || editor.dirty || editor.conflict) throw new Error('conflict');
      const card = await api<Card>(`/drafts/${loadedID}/publish`, 'POST', {
        revision: editor.revision,
        rights
      });
      if (draft) draft = { ...draft, card };
      published = 'Пакет отправлен на проверку. После одобрения он появится в каталоге.';
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function withdraw() {
    busy = true;
    try {
      await api(`/drafts/${loadedID}/withdraw`, 'POST');
      if (draft)
        draft = {
          ...draft,
          card: {
            ...draft.card,
            status: ['published', 'pending'].includes(draft.card.status)
              ? 'withdrawn'
              : draft.card.status,
            published_revision: 0,
            pending_revision: 0
          }
        };
      published = 'Публикация снята.';
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function remove() {
    if (!deleteConfirmed) return;
    busy = true;
    try {
      await api(`/drafts/${loadedID}`, 'DELETE');
      dirty = false;
      await goto('/studio');
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function exportPack() {
    busy = true;
    error = '';
    try {
      await save();
      if (editor?.dirty || editor?.saving || editor?.conflict) throw new Error('conflict');
      const response = await fetch(`/api/drafts/${loadedID}/export`);
      if (!response.ok) {
        const result = await response.json();
        throw new Error(result.error || 'unavailable');
      }
      const url = URL.createObjectURL(await response.blob());
      const link = window.document.createElement('a');
      link.href = url;
      link.download = `${document?.name || 'pack'}.siq`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  function downloadLocal() {
    if (!document) return;
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(document, null, 2)], { type: 'application/json' })
    );
    const a = window.document.createElement('a');
    a.href = url;
    a.download = 'local-draft.json';
    a.click();
    URL.revokeObjectURL(url);
  }
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (dirty) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>
<svelte:head><title>{document?.name || 'Редактор'} — Своя игра</title></svelte:head>
<section class="page">
  <div class="breadcrumbs"><a href="/studio">Мастерская</a> / Редактор пакета</div>
  {#if loading}<p class="loading">Открываем пакет…</p>{:else if document && draft}<div
      class="editor-toolbar row between"
    >
      <div>
        <h1>{document.name || 'Новый пакет'}</h1>
        <span class="helper" role="status" aria-live="polite"
          >{saveLabel || 'Все изменения сохранены'} · версия {revision} · {statusText[
            draft.card.status
          ] || draft.card.status}</span
        >
      </div>
      <div class="row">
        <button class="small" disabled={!canUndo || busy} onclick={undo}>↶ Отменить</button><button
          class="small"
          disabled={!dirty || saving || conflict}
          onclick={() => save()}>{saving ? 'Сохраняем…' : 'Сохранить'}</button
        ><button class="small" disabled={busy || saving || conflict} onclick={exportPack}
          >↓ SIQ</button
        >
      </div>
    </div>
    {#if error}<div class="notice error" role="alert" style="margin-bottom:20px">
        {error}
      </div>{/if}{#if conflict}<div class="panel stack" style="margin-bottom:20px">
        <h3>У пакета появилась новая версия</h3>
        <p>
          Ваш текст остаётся в редакторе. Сохраните локальную копию или решите, какой вариант
          оставить.
        </p>
        <div class="row">
          <button onclick={downloadLocal}>Скачать мои изменения</button><button
            disabled={busy}
            onclick={() => resolveConflict(true)}>Сохранить мой вариант поверх новой версии</button
          ><button disabled={busy} onclick={() => resolveConflict(false)}
            >Загрузить сохранённую версию</button
          >
        </div>
      </div>{/if}
    <div class="editor-layout">
      <aside class="panel editor-sidebar">
        <h3>Структура пакета</h3>
        {#each document.rounds as round, ri}<div class="tree-round">
            <label
              >Название раунда<input
                class="inline-title"
                value={round.name}
                oninput={(event) =>
                  change((next) => (next.rounds[ri].name = event.currentTarget.value))}
              /></label
            >
            <div class="structure-order">
              <ReorderControls
                label={`Раунд ${ri + 1} «${round.name || 'Без названия'}»`}
                index={ri}
                count={document.rounds.length}
                onmove={(delta) =>
                  change((next) => {
                    next.rounds = moveItem(next.rounds, ri, ri + delta);
                  })}
              />
            </div>
            <label
              >Тип раунда<select
                value={round.type || 'standard'}
                onchange={(event) =>
                  change((next) => (next.rounds[ri].type = event.currentTarget.value))}
                ><option value="standard">Обычный</option><option value="final">Финал</option
                ></select
              ></label
            >
            <div class="row">
              <button class="small ghost" onclick={() => addTheme(ri)}>＋ Тема</button><button
                class="small ghost"
                onclick={() => change((next) => next.rounds.splice(ri, 1))}
                aria-label={`Удалить ${round.name}`}>×</button
              >
            </div>
            {#each round.themes as theme, ti}<div class="tree-theme">
                <label
                  >Тема<input
                    class="inline-title"
                    value={theme.name}
                    oninput={(event) =>
                      change(
                        (next) => (next.rounds[ri].themes[ti].name = event.currentTarget.value)
                      )}
                  /></label
                >
                <div class="structure-order">
                  <ReorderControls
                    label={`Тема ${ti + 1} «${theme.name || 'Без названия'}» (раунд ${ri + 1})`}
                    index={ti}
                    count={round.themes.length}
                    onmove={(delta) =>
                      change((next) => {
                        next.rounds[ri].themes = moveItem(next.rounds[ri].themes, ti, ti + delta);
                      })}
                  />
                </div>
                <div class="question-pills">
                  {#each theme.questions as question}<button
                      class:selected={selected === question.id}
                      onclick={() => (selected = question.id)}
                      aria-label={`Вопрос за ${question.price} в теме ${theme.name}`}
                      aria-pressed={selected === question.id}>{question.price}</button
                    >{/each}<button
                    onclick={() => addQuestion(ri, ti)}
                    aria-label={`Добавить вопрос в тему ${theme.name}`}>＋</button
                  >
                </div>
                <button
                  class="small ghost"
                  style="margin-top:6px"
                  onclick={() => change((next) => next.rounds[ri].themes.splice(ti, 1))}
                  >Удалить тему</button
                >
              </div>{/each}
          </div>{/each}<button
          class="small"
          style="margin-top:20px;width:100%"
          onclick={() => addRound()}>＋ Раунд</button
        ><button class="small" style="margin-top:8px;width:100%" onclick={() => addRound('final')}
          >＋ Финал</button
        >
      </aside>
      <div class="stack">
        <AIAuthoring
          rounds={document.rounds}
          ontheme={(preview, round) => {
            const normalized = normalizeDraft({ ...draft!, document: preview }).document;
            change((next) => appendAITheme(next, normalized, round));
          }}
        />
        <details open>
          <summary>О пакете</summary>
          <div class="form-grid">
            <label class="full"
              >Название<input
                value={document.name}
                oninput={(event) => change((next) => (next.name = event.currentTarget.value))}
              /></label
            ><label
              >Автор<input
                value={(document.info.authors || []).join(', ')}
                onchange={(event) =>
                  change(
                    (next) =>
                      (next.info.authors = event.currentTarget.value
                        .split(',')
                        .map((value) => value.trim())
                        .filter(Boolean))
                  )}
              /></label
            ><label
              >Язык<input
                value={document.language || 'ru'}
                maxlength="16"
                oninput={(event) => change((next) => (next.language = event.currentTarget.value))}
              /></label
            ><label
              >Сложность (0–10)<input
                type="number"
                min="0"
                max="10"
                value={document.difficulty || 0}
                oninput={(event) =>
                  change((next) => (next.difficulty = Number(event.currentTarget.value)))}
              /></label
            ><label
              >Возрастная отметка<input
                value={document.restriction || ''}
                placeholder="Например: 12+"
                oninput={(event) =>
                  change((next) => (next.restriction = event.currentTarget.value))}
              /></label
            ><label class="full"
              >Теги через запятую<input
                value={(document.tags || []).join(', ')}
                onchange={(event) =>
                  change(
                    (next) =>
                      (next.tags = event.currentTarget.value
                        .split(',')
                        .map((value) => value.trim())
                        .filter(Boolean))
                  )}
              /></label
            ><label class="full"
              >Описание<textarea
                value={document.info.comments || ''}
                oninput={(event) =>
                  change((next) => (next.info.comments = event.currentTarget.value))}
              ></textarea></label
            >
          </div>
        </details>
        {#if selectedQuestion}<article class="panel stack">
            <div class="row between">
              <div>
                <p class="helper">{selectedLocation}</p>
                <h2>Вопрос за {selectedQuestion.price}</h2>
              </div>
              <button class="small" aria-pressed={preview} onclick={() => (preview = !preview)}
                >{preview ? 'Редактировать' : 'Предпросмотр'}</button
              >
            </div>
            {#if selectedPosition}<ReorderControls
                label={`Вопрос ${selectedPosition.qi + 1} за ${selectedQuestion.price} в теме «${selectedPosition.theme.name || 'Без названия'}»`}
                index={selectedPosition.qi}
                count={selectedPosition.theme.questions.length}
                onmove={moveQuestion}
              />{/if}
            {#if preview}{#key previewKey(draft.card.id, revision, selectedQuestion.id)}<QuestionPreview
                  question={selectedQuestion}
                  assets={draft.assets || []}
                  packId={draft.card.id}
                  {revision}
                />{/key}{:else}<QuestionEditor
                question={selectedQuestion}
                assets={draft.assets || []}
                onchange={updateQuestion}
              /><button class="small danger" onclick={deleteQuestion}>Удалить вопрос</button>{/if}
          </article>{:else}<div class="empty">
            <h3>Выберите вопрос слева</h3>
            <p>Или добавьте раунд, чтобы начать работу.</p>
            <button onclick={() => addRound()}>Добавить раунд</button>
          </div>{/if}{#if draft.issues?.length}<details open>
            <summary>Проверка пакета · {draft.issues.length}</summary>
            <ul class="issues">
              {#each draft.issues as issue}<li>
                  <strong>{issue.severity === 'error' ? 'Ошибка' : 'Замечание'}:</strong>
                  {issue.message} <span class="helper">({issue.path})</span>
                </li>{/each}
            </ul>
            <p class="helper">Проверка обновится после сохранения.</p>
          </details>{/if}
        {#if draft.play_issues?.length}<details open>
            <summary>Готовность к игре · {draft.play_issues.length}</summary>
            <p class="helper">
              Исправьте ошибки ниже, чтобы начать игру. Эта проверка относится только к игре.
            </p>
            <ul class="issues">
              {#each draft.play_issues as issue}<li>
                  <strong>{issue.severity === 'error' ? 'Ошибка' : 'Замечание'}:</strong>
                  {issue.message} <span class="helper">({issue.path})</span>
                </li>{/each}
            </ul>
          </details>{/if}
        {#key draft.card.id}<ModerationHistory packId={draft.card.id} />{/key}
        <details>
          <summary>История версий</summary>
          <div class="list scroll-list">
            {#each history as version}<div class="row between">
                <div>
                  Версия {version.revision}<span class="helper">
                    · {new Date(version.updated_at).toLocaleString('ru-RU')}</span
                  >
                </div>
                <button
                  class="small"
                  disabled={busy || version.revision === revision}
                  onclick={() => restore(version.revision)}>Восстановить</button
                >
              </div>{/each}
          </div>
          <p class="helper" style="margin-top:16px">
            Восстановление создаёт новую версию. Предыдущие остаются в истории.
          </p>
        </details>
        <details>
          <summary>Публикация в каталоге</summary>
          <form class="stack" onsubmit={publish}>
            <p class="muted">
              Черновик виден только вам. Для публикации заполните сведения о правах и отправьте
              пакет на проверку.
            </p>
            <label
              >Лицензия или основание использования<input
                bind:value={rights.license}
                required
                placeholder="Например: CC BY 4.0 или собственное произведение"
              /></label
            ><label
              >Источник<input
                bind:value={rights.source_url}
                required
                placeholder="https://… или original"
              /></label
            ><label
              >Авторство и атрибуция<textarea bind:value={rights.attribution} required
              ></textarea></label
            ><label class="check"
              ><input type="checkbox" bind:checked={rights.allow_download} />Разрешить скачивание
              SIQ</label
            ><label class="check"
              ><input type="checkbox" bind:checked={rights.confirmed} required />Я подтверждаю, что
              имею право публиковать вопросы и все материалы этого пакета.</label
            ><button class="primary" disabled={busy || saving || conflict || !rights.confirmed}
              >Отправить на проверку</button
            >{#if canWithdraw(draft.card)}<button type="button" onclick={withdraw} disabled={busy}
                >Снять публикацию</button
              >{/if}{#if published}<div class="notice success" role="status">{published}</div>{/if}
          </form>
        </details>
        <details>
          <summary>Сыграть в этот пакет</summary>{#if dirty || saving}<p class="notice">
              Сначала сохраните изменения.
            </p>{:else}<CreateRoom
              packId={draft.card.id}
              {revision}
              disabled={!canPlayDraft(draft)}
            />{/if}
        </details>
        <MediaLibrary assets={draft.assets} packId={draft.card.id} {revision} />
        <UsagePanel refreshTrigger={usageRefresh} packRevision={revision} />
        <MediaUpload
          onusagechange={() => usageRefresh++}
          packId={draft.card.id}
          disabled={conflict || busy}
          onflush={flushDraft}
          onattach={attachMedia}
        />
        <details>
          <summary>Файлы и управление</summary>
          <div class="stack">
            {#if draft.original_key}<a
                class="button"
                href={`/api/drafts/${draft.card.id}/original`}
                download>Скачать исходный SIQ</a
              >{/if}
            <p class="helper">
              Медиафайлов: {draft.assets?.length || 0}. Исходники включаются в экспорт SIQ.
            </p>
            <label class="check"
              ><input type="checkbox" bind:checked={deleteConfirmed} />Удалить пакет и снять его
              публикацию</label
            ><button class="danger" disabled={busy || !deleteConfirmed} onclick={remove}
              >Удалить пакет</button
            >
          </div>
        </details>
      </div>
    </div>{:else}<div class="notice error" role="alert">
      {error || 'Не удалось открыть пакет.'} <a class="text-link" href="/login">Войти</a>
    </div>{/if}
</section>

<style>
  .structure-order {
    margin: 8px 0 12px;
  }
</style>
