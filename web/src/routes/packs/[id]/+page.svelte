<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { contextLink, roomContext } from '#lib/appearance.ts';
  const room = $derived(roomContext(page.url));
  import CreateRoomDialog from '#lib/CreateRoomDialog.svelte';
  let creating = $state(false);
  onMount(() => { creating = page.url.searchParams.get('create') === '1'; });
  import { api, bytes, errorText } from '#lib/api.ts';
  import { user } from '#lib/session.ts';
  let { data } = $props();
  let reason = $state(''),
    sent = $state(false),
    error = $state(''),
    busy = $state(false);
  async function report(event: SubmitEvent) {
    event.preventDefault();
    if (!data.card) return;
    busy = true;
    try {
      await api(`/catalogue/${data.card.id}/reports`, 'POST', {
        revision: data.card.revision,
        reason
      });
      sent = true;
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>{data.card?.name || 'Пакет'} — Своя игра</title></svelte:head>
<section class="page">
  <div class="breadcrumbs"><a href={contextLink('/packs', room)}>Каталог</a> / Пакет вопросов</div>
  {#if data.card}{@const card = data.card}
    <div class="two-columns">
      <div>
        <p class="eyebrow">Пакет вопросов · {card.language || 'ru'}</p>
        <h1>{card.name}</h1>
        <p class="muted">{card.author} · версия {card.revision}</p>
        <div class="row">
          {#each card.tags || [] as tag}<a
              class="tag"
              href={contextLink(`/packs?tag=${encodeURIComponent(tag)}`, room)}>{tag}</a
            >{/each}{#if card.restriction}<span class="tag">{card.restriction}</span>{/if}
        </div>
        <div class="stat-grid">
          <div><strong>{card.question_count}</strong><span>вопросов</span></div>
          <div><strong>{card.round_count}</strong><span>раундов</span></div>
          <div><strong>{card.difficulty || '—'}</strong><span>сложность из 10</span></div>
        </div>
        <div class="stack">
          <p class="muted">
            {card.has_media ? 'Содержит изображения, аудио или видео.' : 'Текстовые вопросы.'} Размер:
            {bytes(card.size_bytes)}.
          </p>
          {#if card.allow_download}<a
              class="button"
              href={`/api/catalogue/${card.id}/download`}
              download>↓ Скачать пакет SIQ</a
            >{/if}
          <div class="panel subtle">
            <h3>Авторство и права</h3>
            <p>{card.rights?.attribution || card.author}</p>
            <p class="helper">{card.rights?.license}</p>
            {#if card.rights?.source_url && /^https?:\/\//.test(card.rights.source_url)}<a
                class="text-link"
                href={card.rights.source_url}
                target="_blank"
                rel="noopener noreferrer">Источник ↗</a
              >{/if}
          </div>
          {#if $user}<details>
              <summary>Сообщить о проблеме</summary>{#if sent}<p role="status">
                  Жалоба отправлена на рассмотрение.
                </p>{:else}<form class="stack" onsubmit={report}>
                  <label
                    >Что не так с пакетом?<textarea
                      bind:value={reason}
                      required
                      maxlength="2000"
                      placeholder="Опишите проблему или нарушение прав"></textarea></label
                  >{#if error}<p class="notice error" role="alert">{error}</p>{/if}<button
                    disabled={busy}>Отправить жалобу</button
                  >
                </form>{/if}
            </details>{/if}
        </div>
      </div>
      <aside class="panel stack"><h2>Провести игру</h2><p class="muted">Создайте комнату с этим пакетом и пригласите участников.</p><button class="primary" onclick={() => creating = true}>Создать комнату</button></aside>
    </div>{:else}<div class="empty">
      <h1>Пакет недоступен</h1>
      <p>{errorText(data.error)}</p>
      <a class="button" href={contextLink('/packs', room)}>В каталог</a>
    </div>{/if}
</section>

{#if creating && data.card}<CreateRoomDialog initialPack={data.card} onclose={() => creating = false} />{/if}
