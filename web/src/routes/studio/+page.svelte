<script lang="ts">
  import UsagePanel from '#lib/UsagePanel.svelte';
  import { page } from '$app/state';
  import { contextLink, roomContext } from '#lib/appearance.ts';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, APIError, errorText, bytes } from '#lib/api.ts';
  import PackCard from '#lib/PackCard.svelte';
  import CreateRoom from '#lib/CreateRoom.svelte';
  import type { Card, Draft, Upload } from '#lib/types.ts';
  let cards = $state<Card[]>([]),
    loading = $state(true),
    error = $state(''),
    busy = $state(false),
    upload = $state<Upload | null>(null),
    file = $state<File | null>(null),
    uploading = $state(false),
    login = $state(false);
  let usageRefresh = $state(0);
  let alive = true;
  onMount(() => {
    void load();
    return () => {
      alive = false;
    };
  });
  async function load() {
    try {
      const result = await api<{ items: Card[] | null }>('/drafts');
      cards = result.items || [];
    } catch (e) {
      error = errorText(e);
      login = e instanceof APIError && e.status === 401;
    } finally {
      loading = false;
    }
  }
  async function create() {
    busy = true;
    error = '';
    try {
      const draft = await api<Draft>('/drafts', 'POST', {});
      await goto(contextLink(`/studio/${draft.card.id}`, roomContext(page.url)));
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function uploadFile() {
    if (!file) return;
    const selected = file;
    if (selected.size > 250 * 1024 * 1024) {
      error = 'Размер SIQ не должен превышать 250 МБ.';
      return;
    }
    uploading = true;
    error = '';
    const key = `upload:${selected.name}:${selected.size}:${selected.lastModified}`;
    try {
      let current: Upload;
      const previous = sessionStorage.getItem(key);
      try {
        current = previous
          ? await api<Upload>(`/uploads/${previous}`)
          : await api<Upload>('/uploads', 'POST', { name: selected.name, size: selected.size });
      } catch (e) {
        if (e instanceof APIError && e.status === 404)
          current = await api<Upload>('/uploads', 'POST', {
            name: selected.name,
            size: selected.size
          });
        else throw e;
      }
      sessionStorage.setItem(key, current.id);
      upload = current;
      usageRefresh++;
      while (alive && current.status === 'uploading' && current.offset < selected.size) {
        const response = await fetch(`/api/uploads/${current.id}`, {
          method: 'PATCH',
          headers: {
            'Content-Type': 'application/octet-stream',
            'Upload-Offset': String(current.offset)
          },
          body: selected.slice(
            current.offset,
            Math.min(current.offset + 4 * 1024 * 1024, selected.size)
          )
        });
        if (!response.ok) {
          const result = await response.json().catch(() => ({ error: 'unavailable' }));
          if (response.status === 409) {
            current = await api<Upload>(`/uploads/${current.id}`);
            upload = current;
            continue;
          }
          throw new APIError(result.error, response.status);
        }
        current = await response.json();
        upload = current;
      }
      if (!alive) return;
      if (current.status === 'uploading')
        current = await api<Upload>(`/uploads/${current.id}/complete`, 'POST');
      upload = current;
      while (alive && !['ready', 'failed'].includes(current.status)) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        if (!alive) return;
        current = await api<Upload>(`/uploads/${current.id}`);
        upload = current;
      }
      if (current.status === 'failed') {
        sessionStorage.removeItem(key);
        throw new Error(current.error || 'unavailable');
      }
      if (current.draft_id) {
        sessionStorage.removeItem(key);
        await goto(contextLink(`/studio/${current.draft_id}`, roomContext(page.url)));
      }
    } catch (e) {
      error = errorText(e);
    } finally {
      uploading = false;
      if (alive) usageRefresh++;
    }
  }
</script>

<svelte:head><title>Мастерская — Своя игра</title></svelte:head>
<section class="page">
  <div class="section-head row between">
    <div>
      <p class="eyebrow">Авторская мастерская</p>
      <h1>Хорошие вопросы<br />начинаются здесь.</h1>
      <p>Соберите пакет с нуля или продолжите работу с SIQ.</p>
    </div>
    <button class="primary" onclick={create} disabled={busy || login}>＋ Новый пакет</button>
  </div>
  {#if error}<div class="notice error" role="alert">
      {error}{#if login}
        <a class="text-link" href="/login">Войти</a>{/if}
    </div>{/if}{#if !login}<UsagePanel refreshTrigger={usageRefresh} />
    <div class="two-columns" style="margin:32px 0">
      <div class="panel">
        <h2>Уже есть пакет?</h2>
        <p class="muted">
          Загрузите SIQ, чтобы редактировать вопросы и играть с друзьями. До 250 МБ.
        </p>
        <div class="stack">
          <label
            >Файл SIQ<input
              type="file"
              accept=".siq"
              disabled={uploading}
              onchange={(event) => (file = event.currentTarget.files?.[0] || null)}
            /></label
          ><button onclick={uploadFile} disabled={!file || uploading}
            >{uploading ? 'Загрузка…' : upload ? 'Продолжить загрузку' : 'Загрузить SIQ ↑'}</button
          >{#if upload}<div role="status" aria-live="polite">
              <progress class="progress" max={upload.size || 1} value={upload.offset}></progress>
              <p class="helper">
                {bytes(upload.offset)} из {bytes(upload.size)} · {upload.status === 'uploading'
                  ? 'Загружаем файл'
                  : upload.status === 'failed'
                    ? 'Импорт не удался'
                    : 'Проверяем пакет'}
              </p>
            </div>{/if}
        </div>
      </div>
      <details><summary>Быстро попробовать игру</summary><CreateRoom /></details>
    </div>{/if}
  <div class="section-head">
    <a class="text-link" href={contextLink('/themes', roomContext(page.url))}
      >Мои темы оформления ↗</a
    >
    <h2>Мои пакеты <span class="muted">{cards.length || ''}</span></h2>
  </div>
  {#if loading}<p class="loading" role="status">
      Загружаем ваши пакеты…
    </p>{:else if cards.length}<div class="card-grid">
      {#each cards as card}<PackCard {card} studio />{/each}
    </div>{:else if !login}<div class="empty">
      <div class="empty-symbol" aria-hidden="true">＋</div>
      <h3>Место для вашей первой идеи</h3>
      <p>Начните с одной темы. Вопросы, ответы и медиа можно добавить в редакторе.</p>
      <button onclick={create} disabled={busy}>Создать пакет</button>
    </div>{/if}
</section>
