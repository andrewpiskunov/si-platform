<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from './api.ts';
  import type { DecisionPage, DecisionRecord } from './types.ts';
  import { decisionLabels, mergePage, RequestGate } from './moderation-model.ts';
  let { packId }: { packId: string } = $props();
  let items = $state<DecisionRecord[]>([]),
    next = $state(0),
    busy = $state(false),
    error = $state('');
  const gate = new RequestGate();
  onMount(() => {
    void load();
    return () => gate.dispose();
  });
  async function load(append = false) {
    const token = gate.start();
    busy = true;
    error = '';
    try {
      const result = await api<DecisionPage>(
        `/drafts/${packId}/decisions?after_id=${append ? next : 0}&limit=50`
      );
      gate.apply(token, () => {
        items = mergePage(items, result.items, append);
        next = result.next_after_id || 0;
      });
    } catch (e) {
      gate.apply(token, () => {
        error = errorText(e);
      });
    } finally {
      gate.apply(token, () => {
        busy = false;
      });
    }
  }
</script>

<section class="panel stack" aria-label="История решений" data-testid="moderation-history">
  <div class="row between">
    <h2>История решений</h2>
    <button disabled={busy} onclick={() => load()}>Обновить историю решений</button>
  </div>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#each items as item (item.id)}
    <article class="notice">
      <strong>{decisionLabels[item.decision] || item.decision} · версия {item.revision}</strong>
      <p class="helper">{new Date(item.created_at).toLocaleString('ru-RU')}</p>
      <p class="reason">{item.reason || 'Причина не указана.'}</p>
    </article>
  {:else}<p>{busy ? 'Загружаем историю…' : 'Решений пока нет.'}</p>{/each}
  {#if next}<button disabled={busy} onclick={() => load(true)}>Следующие решения (до 50)</button
    >{/if}
</section>

<style>
  .reason {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
