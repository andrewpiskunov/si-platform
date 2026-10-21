<script lang="ts">
  import UsagePanel from './UsagePanel.svelte';
  import { onMount } from 'svelte';
  import { api, APIError, errorText } from './api.ts';
  import type { ThemeCard, ThemeVersion } from './appearance.ts';
  import ThemeUpload from './ThemeUpload.svelte';
  import ThemePreview from './ThemePreview.svelte';
  let cards = $state<ThemeCard[]>([]),
    version = $state<ThemeVersion | null>(null),
    error = $state(''),
    loading = $state(true),
    login = $state(false),
    deleting = $state(false),
    confirmDelete = $state(false);
  let usageRefresh = $state(0);
  let request: AbortController | undefined;
  onMount(() => {
    void load();
    return () => request?.abort();
  });
  async function load() {
    error = '';
    try {
      cards = (await api<{ themes: ThemeCard[] }>('/themes')).themes || [];
    } catch (e) {
      login = e instanceof APIError && e.status === 401;
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  async function select(id: string, revision?: number) {
    request?.abort();
    request = new AbortController();
    const signal = request.signal;
    error = '';
    confirmDelete = false;
    try {
      const result = await api<ThemeVersion>(
        `/themes/${id}${revision ? `?revision=${revision}` : ''}`,
        'GET',
        undefined,
        undefined,
        signal
      );
      if (!signal.aborted) version = result;
    } catch (e) {
      if (!signal.aborted) error = errorText(e);
    }
  }
  async function remove() {
    if (!version) return;
    deleting = true;
    error = '';
    try {
      await api(`/themes/${version.card.id}`, 'DELETE');
      version = null;
      usageRefresh++;
      confirmDelete = false;
      await load();
    } catch (e) {
      error = errorText(e);
    } finally {
      deleting = false;
    }
  }
</script>

{#if error}<p class="notice error" role="alert">
    {error}{#if login}
      <a class="text-link" href="/login">Войти</a>{/if}
  </p>{/if}
{#if !login}<UsagePanel
    refreshTrigger={usageRefresh}
    themeRevision={version
      ? (cards.find((card) => card.id === version?.card.id)?.revision ?? version.card.revision)
      : undefined}
  /><ThemeUpload
    onusagechange={() => usageRefresh++}
    {cards}
    onsaved={(saved) => {
      version = saved;
      usageRefresh++;
      void load();
    }}
  />
  <div class="section-head row between">
    <h2>Мои темы</h2>
    <button class="small" onclick={load}>Обновить библиотеку</button>
  </div>
  {#if loading}<p role="status">Загружаем темы…</p>{:else if !cards.length}<p class="empty">
      У вас пока нет своих тем. Начните с примера ZIP.
    </p>{:else}<div class="theme-library-list">
      {#each cards as card}<button
          type="button"
          class:selected={version?.card.id === card.id}
          onclick={() => select(card.id)}
          ><strong>{card.name}</strong><span class="helper"
            >{card.author} · версия {card.revision}{card.blocked ? ' · заблокирована' : ''}</span
          ></button
        >{/each}
    </div>{/if}
  {#if version}<section class="panel stack theme-version">
      <div class="row between">
        <h2>{version.card.name}</h2>
        <label
          >Версия<select
            aria-label="Версия оформления"
            value={version.card.revision}
            onchange={(event) => select(version!.card.id, Number(event.currentTarget.value))}
            >{#each Array.from({ length: cards.find((card) => card.id === version!.card.id)?.revision || version.card.revision }, (_, i) => i + 1) as revision}<option
                value={revision}>{revision}</option
              >{/each}</select
          ></label
        >
      </div>
      <p>Автор: {version.prepared.manifest.author}</p>
      <p class="helper theme-provenance">{version.prepared.manifest.provenance}</p>
      <ThemePreview {version} />
      <div class="row">
        <a
          class="text-link"
          href={`/api/themes/${version.card.id}/archive?revision=${version.card.revision}`}
          download>Скачать исходный ZIP</a
        ><button class="danger small" onclick={() => (confirmDelete = true)}>Удалить тему</button>
      </div>
      {#if confirmDelete}<div class="notice">
          <p>
            Тема исчезнет из вашей библиотеки. Уже созданные комнаты сохранят выбранную версию.
            Сохранённые версии продолжат занимать квоту хранения и число оформлений.
          </p>
          <div class="row">
            <button class="danger" disabled={deleting} onclick={remove}
              >Подтвердить удаление темы</button
            ><button onclick={() => (confirmDelete = false)}>Отмена</button>
          </div>
        </div>{/if}
    </section>{/if}{/if}
