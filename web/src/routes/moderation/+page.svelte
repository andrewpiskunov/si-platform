<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '#lib/api.ts';
  import type { Card, Report, Draft, ReportsPage } from '#lib/types.ts';
  import { normalizeDraft } from '#lib/draft-model.ts';
  import {
    reasonError,
    canResolve,
    mergePage,
    previewKey,
    RequestGate,
    resolutionLabels
  } from '#lib/moderation-model.ts';
  import ModerationHistory from '#lib/ModerationHistory.svelte';
  import QuestionPreview from '#lib/QuestionPreview.svelte';
  let queue = $state<Card[]>([]),
    reports = $state<Report[]>([]);
  let queueError = $state(''),
    reportError = $state(''),
    actionError = $state(''),
    previewError = $state('');
  let queueLoading = $state(false),
    reportLoading = $state(false),
    previewLoading = $state(false),
    busy = $state(false);
  let queueReasons = $state<Record<string, string>>({}),
    reportReasons = $state<Record<string, string>>({});
  let status = $state<'open' | 'resolved'>('open'),
    loadedStatus = $state<'open' | 'resolved'>('open'),
    next = $state('');
  let preview = $state<Draft | null>(null),
    target = $state<{ id: string; revision: number } | null>(null),
    selected = $state('');
  let historyRefresh = $state(0),
    success = $state('');
  const queueGate = new RequestGate(),
    reportGate = new RequestGate(),
    previewGate = new RequestGate(),
    actionGate = new RequestGate();
  const questions = $derived(
    preview?.document.rounds.flatMap((round) =>
      round.themes.flatMap((theme) =>
        theme.questions.map((question) => ({
          question,
          label: `${round.name} / ${theme.name} · ${question.price}`
        }))
      )
    ) || []
  );
  const selectedQuestion = $derived(
    questions.find((item) => item.question.id === selected)?.question
  );
  onMount(() => {
    void loadQueue();
    void loadReports();
    return () => {
      queueGate.dispose();
      reportGate.dispose();
      previewGate.dispose();
      actionGate.dispose();
    };
  });
  async function loadQueue() {
    const token = queueGate.start();
    queueLoading = true;
    queueError = '';
    try {
      const result = await api<{ items: Card[] | null }>('/moderation');
      queueGate.apply(token, () => {
        queue = result.items || [];
      });
    } catch (e) {
      queueGate.apply(token, () => {
        queueError = errorText(e);
      });
    } finally {
      queueGate.apply(token, () => {
        queueLoading = false;
      });
    }
  }
  async function loadReports(append = false) {
    const token = reportGate.start(),
      requestedStatus = status;
    const cursor = append && loadedStatus === requestedStatus ? next : '';
    reportLoading = true;
    reportError = '';
    try {
      const result = await api<ReportsPage>(
        `/reports?status=${requestedStatus}&limit=50${cursor ? `&after=${cursor}` : ''}`
      );
      reportGate.apply(token, () => {
        reports = mergePage(reports, result.items, !!cursor);
        loadedStatus = requestedStatus;
        next = result.next_after || '';
      });
    } catch (e) {
      reportGate.apply(token, () => {
        reportError = errorText(e);
      });
    } finally {
      reportGate.apply(token, () => {
        reportLoading = false;
      });
    }
  }
  async function decide(id: string, revision: number, decision: string, reason: string) {
    if (busy) return;
    actionError = reasonError(reason, decision !== 'approve');
    if (actionError) return;
    const token = actionGate.start();
    busy = true;
    success = '';
    try {
      await api(`/moderation/${id}`, 'POST', { revision, decision, reason });
      actionGate.apply(token, () => {
        success = 'Решение сохранено.';
        historyRefresh++;
        if (decision === 'block' && target?.id === id) closePreview();
        void loadQueue();
        void loadReports();
      });
    } catch (e) {
      actionGate.apply(token, () => {
        actionError = errorText(e);
      });
    } finally {
      actionGate.apply(token, () => {
        busy = false;
      });
    }
  }
  async function resolveReport(report: Report, disposition: 'dismissed' | 'resolved') {
    if (!canResolve(report.status, busy)) return;
    const reason = reportReasons[report.id] || '';
    actionError = reasonError(reason);
    if (actionError) return;
    const token = actionGate.start();
    busy = true;
    success = '';
    try {
      const result = await api<Report>(`/reports/${report.id}/resolution`, 'POST', {
        disposition,
        reason
      });
      actionGate.apply(token, () => {
        // Retain the accepted terminal result if the subsequent list refresh fails.
        reports = reports.map((item) => (item.id === result.id ? result : item));
        success = 'Решение по жалобе сохранено.';
        void loadReports();
      });
    } catch (e) {
      actionGate.apply(token, () => {
        actionError = errorText(e);
      });
    } finally {
      actionGate.apply(token, () => {
        busy = false;
      });
    }
  }
  function closePreview() {
    previewGate.start();
    preview = null;
    target = null;
    selected = '';
    previewError = '';
    previewLoading = false;
  }
  async function inspect(id: string, revision: number) {
    const token = previewGate.start();
    target = { id, revision };
    preview = null;
    selected = '';
    previewError = '';
    previewLoading = true;
    try {
      const result = normalizeDraft(await api<Draft>(`/drafts/${id}?revision=${revision}`));
      previewGate.apply(token, () => {
        preview = result;
        selected =
          result.document.rounds.flatMap((round) =>
            round.themes.flatMap((theme) => theme.questions)
          )[0]?.id || '';
      });
    } catch (e) {
      previewGate.apply(token, () => {
        previewError = errorText(e);
      });
    } finally {
      previewGate.apply(token, () => {
        previewLoading = false;
      });
    }
  }
</script>

<svelte:head><title>Модерация — Своя игра</title></svelte:head>
<section class="page">
  <p class="eyebrow">Модерация</p>
  <h1>Проверка публикаций</h1>
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}
  {#if success}<p class="notice success" role="status">{success}</p>{/if}
  <div class="two-columns">
    <section class="stack" aria-label="Очередь публикаций">
      <div class="row between">
        <h2>На проверке · {queue.length}</h2>
        <button disabled={queueLoading} onclick={loadQueue}>Обновить очередь</button>
      </div>
      {#if queueError}<p class="notice error" role="alert">{queueError}</p>{/if}
      {#if queueLoading}<p role="status">Загружаем очередь…</p>{/if}
      {#each queue as card (`${card.id}:${card.revision}`)}<article
          class="panel stack"
          data-pack-id={card.id}
        >
          <div>
            <h3>{card.name}</h3>
            <p class="helper">
              {card.author} · версия {card.revision} · {card.question_count} вопросов
            </p>
            <p>{card.rights?.license} · {card.rights?.attribution}</p>
            <p class="helper">Источник: {card.rights?.source_url}</p>
          </div>
          <button onclick={() => inspect(card.id, card.revision)}>Просмотреть вопросы</button>
          <label
            >Причина решения<textarea bind:value={queueReasons[`${card.id}:${card.revision}`]}
            ></textarea></label
          >
          <div class="row">
            <button
              class="primary"
              disabled={busy}
              onclick={() =>
                decide(
                  card.id,
                  card.revision,
                  'approve',
                  queueReasons[`${card.id}:${card.revision}`] || ''
                )}>Опубликовать</button
            >
            <button
              disabled={busy}
              onclick={() =>
                decide(
                  card.id,
                  card.revision,
                  'reject',
                  queueReasons[`${card.id}:${card.revision}`] || ''
                )}>Вернуть автору</button
            >
            <button
              class="danger"
              disabled={busy}
              onclick={() =>
                decide(
                  card.id,
                  card.revision,
                  'block',
                  queueReasons[`${card.id}:${card.revision}`] || ''
                )}>Заблокировать пакет</button
            >
          </div>
        </article>{:else}{#if !queueLoading}<p class="muted">Новых заявок нет.</p>{/if}{/each}
    </section>
    <section class="stack" aria-label="Жалобы">
      <div class="row between">
        <h2>Жалобы · {reports.length}</h2>
        <button disabled={reportLoading} onclick={() => loadReports()}>Обновить жалобы</button>
      </div>
      <label
        >Статус жалоб<select
          value={status}
          onchange={(event) => {
            status = event.currentTarget.value as 'open' | 'resolved';
            void loadReports();
          }}
          ><option value="open">Открытые</option><option value="resolved">Рассмотренные</option
          ></select
        ></label
      >
      {#if reportError}<p class="notice error" role="alert">
          {reportError} Сохранён предыдущий список.
        </p>{/if}
      {#if loadedStatus !== status && reports.length}<p class="helper">
          Показан предыдущий список: {loadedStatus === 'open' ? 'открытые' : 'рассмотренные'}.
        </p>{/if}
      {#if reportLoading}<p role="status">Загружаем жалобы…</p>{/if}
      {#each reports as report (report.id)}<article class="panel stack" data-report-id={report.id}>
          <a class="text-link" href={`/packs/${report.pack_id}`}>Открыть пакет ↗</a>
          <p class="reason">{report.reason}</p>
          <p class="helper">
            Версия {report.revision} · {new Date(report.created_at).toLocaleString('ru-RU')} · {report.status ===
            'open'
              ? 'Открыта'
              : 'Рассмотрена'}
          </p>
          <button onclick={() => inspect(report.pack_id, report.revision)}
            >Просмотреть версию {report.revision}</button
          >
          {#if report.status === 'open'}
            <label
              >Причина решения по жалобе<textarea bind:value={reportReasons[report.id]} required
              ></textarea></label
            >
            <div class="row">
              <button
                disabled={!canResolve(report.status, busy)}
                onclick={() => resolveReport(report, 'dismissed')}>Отклонить жалобу</button
              >
              <button
                disabled={!canResolve(report.status, busy)}
                onclick={() => resolveReport(report, 'resolved')}>Завершить рассмотрение</button
              >
              <button
                class="danger"
                disabled={busy}
                onclick={() =>
                  decide(report.pack_id, report.revision, 'block', reportReasons[report.id] || '')}
                >Заблокировать пакет</button
              >
            </div>
          {:else}
            <strong>{resolutionLabels[report.resolution] || 'Рассмотрена'}</strong>
            <p class="reason">{report.resolution_reason || 'Причина не сохранена.'}</p>
            <p class="helper">
              Исполнитель: {report.resolved_by || 'неизвестен'} · {report.resolved_at
                ? new Date(report.resolved_at).toLocaleString('ru-RU')
                : 'время неизвестно'}
            </p>
          {/if}
        </article>{:else}{#if !reportLoading}<p class="muted">
            Жалоб в этой категории нет.
          </p>{/if}{/each}
      {#if next && loadedStatus === status}<button
          disabled={reportLoading}
          onclick={() => loadReports(true)}>Следующие жалобы (до 50)</button
        >{/if}
    </section>
  </div>
  {#if target}<section
      class="panel stack preview"
      aria-label="Просмотр версии"
      data-testid="moderation-preview"
    >
      <div class="row between">
        <h2>{preview?.document.name || 'Просмотр пакета'} · версия {target.revision}</h2>
        <button onclick={closePreview}>Закрыть просмотр</button>
      </div>
      {#if previewLoading}<p role="status">Загружаем версию…</p>{/if}
      {#if previewError}<p class="notice error" role="alert">{previewError}</p>
        <button onclick={() => target && inspect(target.id, target.revision)}
          >Повторить загрузку версии</button
        >{/if}
      {#if preview}
        <label
          >Вопрос для проверки<select bind:value={selected}
            >{#each questions as item}<option value={item.question.id}>{item.label}</option
              >{/each}</select
          ></label
        >
        {#if selectedQuestion}{#key previewKey(target.id, target.revision, selectedQuestion.id)}<QuestionPreview
              question={selectedQuestion}
              assets={preview.assets}
              packId={target.id}
              revision={target.revision}
            />{/key}{:else}<p>В этой версии нет вопросов.</p>{/if}
      {/if}
      {#key `${target.id}:${historyRefresh}`}<ModerationHistory packId={target.id} />{/key}
    </section>{/if}
</section>

<style>
  .preview {
    margin-top: 24px;
  }
  .reason {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .stack {
    min-width: 0;
  }
  .helper {
    overflow-wrap: anywhere;
  }
</style>
