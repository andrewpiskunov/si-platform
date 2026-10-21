<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, errorText } from './api.ts';
  import { user, sessionEpoch } from './session.ts';
  import { UsageRequestScope } from './usage-model.ts';
  import {
    exportURL,
    type ExportKind,
    type ExportSubject,
    type ExportSubjects
  } from './account-export-model.ts';

  let kind = $state<ExportKind>('all');
  let selected = $state('');
  let metadataOnly = $state(false);
  let items = $state<ExportSubject[]>([]);
  let cursor = $state<string | null>(null);
  let pending = $state(false);
  let error = $state('');
  let mounted = $state(false);
  const scope = new UsageRequestScope();
  const identity = () => `${$user?.id ?? ''}:${$sessionEpoch}:${kind}`;
  const download = $derived(exportURL(kind, selected, metadataOnly));

  onMount(() => {
    mounted = true;
    return () => scope.dispose();
  });
  $effect(() => {
    kind;
    $user?.id;
    $sessionEpoch;
    if (!mounted) return;
    untrack(() => {
      scope.begin(identity());
      selected = '';
      items = [];
      cursor = null;
      error = '';
      pending = false;
      if ($user && kind !== 'all') void load();
    });
  });

  async function load(after = '') {
    if (kind === 'all' || !$user) return;
    const request = scope.begin(identity());
    // Lifecycle/generation checks must run before reading component/store getters.
    const current = () => scope.current(request, identity);
    pending = true;
    error = '';
    try {
      const query = new URLSearchParams({ kind });
      if (after) query.set('after', after);
      const result = await api<ExportSubjects>(
        `/privacy/subjects?${query}`,
        'GET',
        undefined,
        undefined,
        request.signal
      );
      if (!current()) return;
      items = after ? [...items, ...result.items] : result.items;
      cursor = result.next_cursor || null;
    } catch (e) {
      if (!current()) return;
      error = errorText(e);
    } finally {
      if (current()) pending = false;
    }
  }
</script>

<section class="panel stack account-export" aria-labelledby="account-export-title">
  <h2 id="account-export-title">Ваши данные</h2>
  <p class="muted">
    ZIP с данными профиля, собственными материалами, всеми сохранёнными версиями и файлами, включая
    сохранённые удалённые материалы. Сначала сохраните изменения в редакторе: выгрузка отражает
    состояние на начало подготовки.
  </p>
  <label
    >Состав выгрузки
    <select bind:value={kind}>
      <option value="all">Весь аккаунт</option>
      <option value="pack">Один пакет</option>
      <option value="theme">Одно оформление</option>
      <option value="upload">Одна загрузка</option>
    </select>
  </label>
  {#if kind !== 'all'}
    <label
      >Материал (включая удалённые)
      <select bind:value={selected} disabled={!items.length}>
        <option value="">Выберите материал</option>
        {#each items as item (item.id)}
          <option value={item.id}>{item.name || item.id}{item.deleted ? ' — удалён' : ''}</option>
        {/each}
      </select>
    </label>
    <div aria-live="polite">
      {#if pending}<p class="helper" role="status">Загружаем список…</p>{/if}
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      {#if !pending && !error && !items.length}<p class="helper">
          Сохранённых материалов этого типа нет.
        </p>{/if}
    </div>
    {#if error}
      <button type="button" disabled={pending} onclick={() => load(cursor || '')}
        >Повторить загрузку списка</button
      >
    {:else if cursor}
      <button type="button" disabled={pending} onclick={() => load(cursor || '')}
        >Показать ещё 50</button
      >
    {/if}
    <p class="helper">
      Выбранный материал включает все его версии. Профиль и собственные действия аккаунта входят в
      каждый вариант.
    </p>
  {/if}
  <label class="check"
    ><input type="checkbox" bind:checked={metadataOnly} />Только метаданные, без файлов</label
  >
  {#if download}
    <a class="button primary" href={download} target="_blank" rel="noopener" data-sveltekit-reload
      >↓ Скачать ZIP</a
    >
  {:else}
    <button type="button" disabled>Выберите материал для скачивания</button>
  {/if}
  <p class="helper">
    Подготовка откроется в новой вкладке; затем браузер предложит сохранить ZIP. Лимиты: 20 ГиБ, 100
    000 объектов, 15 минут. Если аккаунт слишком большой, выберите один материал или только
    метаданные.
  </p>
  <p class="helper">
    Заблокированные файлы исключены; причины пропусков указаны в архиве. Состояния комнат, данные
    других игроков, пароли и секреты токенов не входят в выгрузку.
  </p>
  <a href="/api/account/export" download>Скачать только профиль и сведения о токенах (JSON)</a>
</section>

<style>
  .account-export {
    min-width: 0;
  }
  select {
    width: 100%;
    min-width: 0;
  }
  a,
  p {
    overflow-wrap: anywhere;
  }
</style>
