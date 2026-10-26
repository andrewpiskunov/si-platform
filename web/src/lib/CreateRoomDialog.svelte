<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from './api.ts';
  import type { Card } from './types.ts';
  import CreateRoom from './CreateRoom.svelte';
  let { initialPack = null, onclose }: { initialPack?: Card | null; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let selected = $state<Card | 'demo' | null>(null);
  let choosing = $state(true);
  let query = $state('');
  let items = $state<Card[]>([]);
  let loading = $state(true);
  let error = $state('');
  let offset = $state(0);
  let creating = $state(false);
  let request = 0;
  const pack = $derived(selected && selected !== 'demo' ? selected : null);
  onMount(() => {
    selected = initialPack;
    choosing = !initialPack;
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    dialog.showModal();
    void load(0);
    return () => {
      ++request;
      document.body.style.overflow = overflow;
      previous?.focus();
    };
  });
  async function load(start: number) {
    const current = ++request;
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ q: query.trim(), offset: String(start) });
      const result = await api<{ items: Card[] | null }>(`/catalogue?${params}`, 'GET', undefined, undefined, AbortSignal.timeout(10000));
      if (current !== request) return;
      items = result.items || [];
      offset = start;
    } catch (e) {
      if (current === request) error = errorText(e);
    } finally {
      if (current === request) loading = false;
    }
  }
  function choose(value: Card | 'demo') {
    selected = value;
    choosing = false;
  }
</script>

<dialog bind:this={dialog} aria-labelledby="create-room-title" oncancel={(event) => { event.preventDefault(); if (!creating) onclose(); }}>
  <header class="row between">
    <div><p class="eyebrow">Новая игра</p><h2 id="create-room-title">Создать комнату</h2></div>
    <button type="button" aria-label="Закрыть окно" disabled={creating} onclick={onclose}>✕</button>
  </header>
  <div class="dialog-content">
    <section class="pack-selection stack" aria-label="Пакет вопросов">
      <h3>1. Пакет вопросов</h3>
      {#if selected}
        <div class="selected-pack stack">
          <strong>Выбран: {pack?.name || 'Демонстрационный пакет'}</strong>
          <span class="helper">{pack ? `${pack.question_count} вопросов · ${pack.round_count} раундов · ${pack.author}` : '17 вопросов · 3 раунда · аукцион, кот, всем, без риска, ставки и финал'}</span>
          <button type="button" disabled={creating} onclick={() => choosing = !choosing}>{choosing ? 'Скрыть выбор пакета' : 'Изменить пакет'}</button>
        </div>
      {/if}
      {#if choosing}
        <form class="row pack-search" onsubmit={(event) => { event.preventDefault(); void load(0); }}>
          <label>Поиск пакетов<input bind:value={query} type="search" placeholder="Название, тема или автор" /></label>
          <button disabled={loading || creating}>Найти</button>
        </form>
        <div class="pack-results" aria-live="polite" aria-busy={loading}>
          {#if loading}<p class="muted">Загружаем пакеты…</p>
          {:else if error}<p role="alert">{error}</p><button type="button" onclick={() => load(offset)}>Повторить загрузку</button>
          {:else}
            {#each items as card (card.id)}
              <button type="button" class="pack-option" disabled={creating} onclick={() => choose(card)}>
                <strong>{card.name}</strong><span class="helper">{card.question_count} вопросов · {card.round_count} раундов · {card.author}</span>
              </button>
            {:else}<p class="muted">Пакеты не найдены</p><p class="helper">Попробуйте другой запрос или используйте демонстрационный пакет.</p>{/each}
            <div class="row">
              {#if offset > 0}<button type="button" onclick={() => load(Math.max(0, offset - 50))}>Предыдущие пакеты</button>{/if}
              {#if items.length === 50}<button type="button" onclick={() => load(offset + 50)}>Следующие пакеты</button>{/if}
            </div>
          {/if}
        </div>
        <button type="button" disabled={creating} onclick={() => choose('demo')}>Выбрать демонстрационный пакет</button>
      {/if}
    </section>
    <section class="room-configuration" aria-label="Настройки комнаты">
      <h3>2. Настройки комнаты</h3>
      <CreateRoom packId={pack?.id || ''} revision={pack?.revision || 0} canCreate={!!selected} showHeading={false} onbusy={(value) => creating = value} loginNext={pack ? `/packs/${pack.id}?create=1` : "/rooms?create=1"} />
    </section>
  </div>
</dialog>

<style>
  dialog { width: min(1050px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 32px); padding: 0; color: var(--text); background: var(--bg); border: 1px solid var(--line); border-radius: 2px; }
  dialog::backdrop { background: rgb(2 9 32 / 80%); }
  header { position: sticky; top: 0; z-index: 2; padding: 24px 28px; border-bottom: 1px solid var(--line); background: var(--surface); }
  header h2, header p, h3 { margin: 0; }
  header h2 { font-size: 28px; }
  .dialog-content { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
  section { padding: 28px; min-width: 0; }
  .pack-selection { align-content: start; border-right: 1px solid var(--line); }
  .room-configuration > h3 { margin-bottom: 20px; }
  .selected-pack { padding: 16px; border-left: 3px solid var(--accent); background: var(--surface); overflow-wrap: anywhere; }
  .pack-search { align-items: end; }
  .pack-search label { flex: 1; min-width: 140px; }
  .pack-results { max-height: 340px; overflow-y: auto; }
  .pack-option { display: flex; flex-direction: column; text-align: left; gap: 6px; width: 100%; margin-bottom: 8px; overflow-wrap: anywhere; white-space: normal; }
  @media (max-width: 760px) {
    .dialog-content { grid-template-columns: 1fr; }
    section, header { position: sticky; top: 0; z-index: 2; padding: 20px; }
    .pack-selection { border-right: 0; border-bottom: 1px solid var(--line); }
  }
</style>
