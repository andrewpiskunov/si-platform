<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '#lib/api.ts';
  import type { Card, Report, Draft } from '#lib/types.ts';
  import { normalizeDraft } from '#lib/draft-model.ts';
  let queue = $state<Card[]>([]),
    reports = $state<Report[]>([]),
    error = $state(''),
    loading = $state(true),
    busy = $state(false),
    reason = $state<Record<string, string>>({}),
    preview = $state<Draft | null>(null);
  onMount(() => {
    void load();
  });
  async function load() {
    try {
      const [q, r] = await Promise.all([
        api<{ items: Card[] | null }>('/moderation'),
        api<{ items: Report[] | null }>('/reports')
      ]);
      queue = q.items || [];
      reports = r.items || [];
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  async function decide(id: string, revision: number, decision: string) {
    if (decision !== 'approve' && !reason[id]?.trim()) {
      error = 'Укажите причину решения.';
      return;
    }
    busy = true;
    error = '';
    try {
      await api(`/moderation/${id}`, 'POST', { revision, decision, reason: reason[id] || '' });
      preview = null;
      await load();
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function inspect(id: string, revision: number) {
    try {
      preview = normalizeDraft(await api<Draft>(`/drafts/${id}?revision=${revision}`));
    } catch (e) {
      error = errorText(e);
    }
  }
</script>

<svelte:head><title>Модерация — Своя игра</title></svelte:head>
<section class="page">
  <p class="eyebrow">Модерация</p>
  <h1>Проверка публикаций</h1>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if loading}<p
      class="loading"
    >
      Загружаем очередь…
    </p>{:else}<div class="two-columns">
      <div class="stack">
        <h2>На проверке · {queue.length}</h2>
        {#each queue as card}<article class="panel stack">
            <div>
              <h3>{card.name}</h3>
              <p class="helper">
                {card.author} · версия {card.revision} · {card.question_count} вопросов
              </p>
              <p>{card.rights?.license} · {card.rights?.attribution}</p>
              <p class="helper">Источник: {card.rights?.source_url}</p>
            </div>
            <button onclick={() => inspect(card.id, card.revision)}>Просмотреть вопросы</button
            ><label
              >Причина решения<textarea bind:value={reason[card.id]} maxlength="2000"
              ></textarea></label
            >
            <div class="row">
              <button
                class="primary"
                disabled={busy}
                onclick={() => decide(card.id, card.revision, 'approve')}>Опубликовать</button
              ><button disabled={busy} onclick={() => decide(card.id, card.revision, 'reject')}
                >Вернуть автору</button
              ><button
                class="danger"
                disabled={busy}
                onclick={() => decide(card.id, card.revision, 'block')}>Заблокировать</button
              >
            </div>
          </article>{:else}<div class="empty">
            <h3>Всё проверено</h3>
            <p>Новые заявки появятся здесь.</p>
          </div>{/each}
      </div>
      <div class="stack">
        <h2>Жалобы · {reports.length}</h2>
        {#each reports as report}<article class="panel stack">
            <a class="text-link" href={`/packs/${report.pack_id}`}>Открыть пакет ↗</a>
            <p>{report.reason}</p>
            <p class="helper">
              Версия {report.revision} · {new Date(report.created_at).toLocaleString('ru-RU')} · {report.status}
            </p>
            <label
              >Причина блокировки<textarea bind:value={reason[report.pack_id]} maxlength="2000"
              ></textarea></label
            ><button
              class="danger"
              disabled={busy}
              onclick={() => decide(report.pack_id, report.revision, 'block')}
              >Заблокировать пакет</button
            >
          </article>{:else}<p class="muted">Жалоб пока нет.</p>{/each}
      </div>
    </div>{/if}{#if preview}<section class="panel stack" style="margin-top:24px">
      <div class="row between">
        <h2>{preview.card.name}</h2>
        <button onclick={() => (preview = null)}>Закрыть просмотр</button>
      </div>
      {#each preview.document.rounds as round}<h3>{round.name}</h3>
        {#each round.themes as theme}<h3>{theme.name}</h3>
          {#each theme.questions as question}<div class="notice">
              <strong>{question.price}</strong
              >{#each question.params.find((parameter) => parameter.name === 'question')?.items || [] as content}<p
                >
                  {content.type}: {content.value}
                </p>{/each}
              <p>Ответ: {question.right.join('; ')}</p>
            </div>{/each}{/each}{/each}
    </section>{/if}
</section>
