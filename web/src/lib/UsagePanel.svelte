<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, APIError } from './api.ts';
  import { user, sessionEpoch } from './session.ts';
  import type { Usage } from './types.ts';
  import { formatUsageBytes, usageFailure, usageLimits, UsageRequestScope } from './usage-model.ts';

  let {
    refreshTrigger = 0,
    packRevision,
    themeRevision
  }: {
    refreshTrigger?: number;
    packRevision?: number;
    themeRevision?: number;
  } = $props();
  let snapshot = $state<Usage | null>(null),
    pending = $state(false),
    error = $state(false),
    stale = $state(false),
    mounted = $state(false);
  let previousAccount: string | null | undefined;
  const scope = new UsageRequestScope();
  onMount(() => {
    mounted = true;
    return () => scope.dispose();
  });
  $effect(() => {
    const account = $user?.id ?? null;
    refreshTrigger;
    $sessionEpoch;
    if (!mounted) return;
    untrack(() => {
      if (account !== previousAccount) {
        snapshot = null;
        stale = false;
        error = false;
        previousAccount = account;
      }
      void refresh();
    });
  });
  async function refresh() {
    const epoch = $sessionEpoch;
    const request = scope.begin($user?.id ?? null);
    const current = () =>
      epoch === $sessionEpoch && scope.current(request, () => $user?.id ?? null);
    pending = true;
    error = false;
    try {
      const result = await api<Usage>('/usage', 'GET', undefined, undefined, request.signal);
      if (!current()) return;
      snapshot = result;
      stale = false;
    } catch (e) {
      if (!current()) return;
      const failed = usageFailure(snapshot, e instanceof APIError ? e.status : undefined);
      snapshot = failed.snapshot;
      stale = failed.stale;
      error = true;
    } finally {
      if (current()) pending = false;
    }
  }
</script>

<section
  class="panel stack quota-usage"
  data-testid="quota-usage"
  aria-label="Лимиты и использование"
>
  <div class="row between">
    <h2>Лимиты и использование</h2>
    <button type="button" class="small" onclick={refresh} disabled={pending}
      >Обновить использование</button
    >
  </div>
  <div aria-live="polite">
    {#if pending}<p class="helper" role="status">Обновляем использование…</p>{/if}
    {#if error}<p class="notice error" role="status">
        Не удалось обновить использование. {stale
          ? 'Показаны неактуальные данные.'
          : 'Текущее использование неизвестно.'}
      </p>{/if}
    <dl class="usage-grid">
      <div>
        <dt>Хранилище</dt>
        <dd data-testid="quota-storage">
          {formatUsageBytes(snapshot?.storage.used_bytes)} из {formatUsageBytes(
            snapshot?.storage.limit_bytes ?? usageLimits.storage
          )} · осталось {formatUsageBytes(snapshot?.storage.remaining_bytes)}
        </dd>
      </div>
      <div>
        <dt>Пакеты</dt>
        <dd data-testid="quota-packs">
          {snapshot?.packs.used ?? 'Неизвестно'} из {snapshot?.packs.limit ?? usageLimits.packs} · осталось
          {snapshot?.packs.remaining ?? 'неизвестно'}
        </dd>
      </div>
      <div>
        <dt>Сохранённые оформления</dt>
        <dd data-testid="quota-themes">
          {snapshot?.themes.used ?? 'Неизвестно'} из {snapshot?.themes.limit ?? usageLimits.themes} ·
          осталось {snapshot?.themes.remaining ?? 'неизвестно'}
        </dd>
      </div>
      <div>
        <dt>Активные загрузки</dt>
        <dd data-testid="quota-uploads">
          {snapshot?.uploads.active ?? 'Неизвестно'} из {snapshot?.uploads.limit ??
            usageLimits.uploads} · свободно {snapshot?.uploads.remaining ?? 'неизвестно'}
        </dd>
      </div>
    </dl>
  </div>
  <p class="helper">
    SIQ — до {formatUsageBytes(usageLimits.siq_bytes)}; медиа — до {formatUsageBytes(
      usageLimits.media_bytes
    )}; ZIP оформления — до {formatUsageBytes(usageLimits.theme_bytes)}.
  </p>
  <p class="helper">
    Версии пакета: {packRevision === undefined ? 'до' : packRevision + ' из'}
    {usageLimits.pack_revisions}. Версии оформления: {themeRevision === undefined
      ? 'до'
      : themeRevision + ' из'}
    {usageLimits.theme_revisions}.
  </p>
  <p class="helper">
    Учитываются сохранённые версии и подготовленные файлы. Размер после обработки может вырасти.
    Остаток не резервирует место.
  </p>
  <p class="helper">
    Удаление оформления не освобождает квоту сразу: фоновая очистка начинается через 30 дней. Версии
    для живых комнат и подготовка выгрузки могут отложить очистку.
    <a href="/privacy">Правила хранения данных</a>.
  </p>
</section>

<style>
  .quota-usage {
    margin: 20px 0;
  }
  .usage-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 230px), 1fr));
    gap: 12px;
    margin: 0;
  }
  dt {
    font-weight: 600;
  }
  dd {
    margin: 4px 0 0;
    overflow-wrap: anywhere;
  }
</style>
