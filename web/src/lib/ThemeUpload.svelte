<script lang="ts">
  import { onMount } from 'svelte';
  import { api, APIError, errorText, bytes } from './api.ts';
  import { usageLimits } from './usage-model.ts';
  import type { Upload } from './types.ts';
  import type { ThemeCard, ThemeVersion } from './appearance.ts';
  let {
    cards,
    onsaved,
    onusagechange
  }: { cards: ThemeCard[]; onsaved: (version: ThemeVersion) => void; onusagechange?: () => void } =
    $props();
  let file = $state<File | null>(null),
    upload = $state<Upload | null>(null),
    busy = $state(false),
    error = $state(''),
    target = $state(''),
    base = $state(0),
    saving = $state(false);
  let controller: AbortController | undefined,
    storageKey = '';
  let alive = true;
  onMount(() => () => {
    alive = false;
    controller?.abort();
  });
  async function start() {
    if (!file || busy) return;
    if (file.size === 0 || file.size > 5 * 1024 * 1024) {
      error = 'ZIP должен быть размером от 1 байта до 5 МБ.';
      return;
    }
    busy = true;
    error = '';
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal,
      selected = file;
    storageKey = `theme-upload:${selected.name}:${selected.size}:${selected.lastModified}`;
    try {
      let current: Upload;
      const previous = sessionStorage.getItem(storageKey);
      try {
        current = previous
          ? await api<Upload>(`/uploads/${previous}`, 'GET', undefined, undefined, signal)
          : await api<Upload>(
              '/theme-uploads',
              'POST',
              { name: selected.name, size: selected.size },
              undefined,
              signal
            );
      } catch (e) {
        if (e instanceof APIError && [404, 410].includes(e.status))
          current = await api<Upload>(
            '/theme-uploads',
            'POST',
            { name: selected.name, size: selected.size },
            undefined,
            signal
          );
        else throw e;
      }
      sessionStorage.setItem(storageKey, current.id);
      upload = current;
      if (alive) onusagechange?.();
      while (current.status === 'uploading' && current.offset < selected.size) {
        const response = await fetch(`/api/uploads/${current.id}`, {
          method: 'PATCH',
          headers: {
            'Content-Type': 'application/octet-stream',
            'Upload-Offset': String(current.offset)
          },
          body: selected.slice(
            current.offset,
            Math.min(current.offset + 4 * 1024 * 1024, selected.size)
          ),
          signal
        });
        if (response.status === 409) {
          current = await api<Upload>(
            `/uploads/${current.id}`,
            'GET',
            undefined,
            undefined,
            signal
          );
          continue;
        }
        if (!response.ok) throw new Error('unavailable');
        current = await response.json();
        upload = current;
      }
      if (current.status === 'uploading')
        current = await api<Upload>(
          `/uploads/${current.id}/complete`,
          'POST',
          {},
          undefined,
          signal
        );
      upload = current;
      while (!['ready', 'failed'].includes(current.status)) {
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(() => {
            signal.removeEventListener('abort', abort);
            resolve();
          }, 1000);
          const abort = () => {
            clearTimeout(timer);
            reject(new DOMException('Aborted', 'AbortError'));
          };
          signal.addEventListener('abort', abort, { once: true });
        });
        current = await api<Upload>(`/uploads/${current.id}`, 'GET', undefined, undefined, signal);
        upload = current;
      }
      if (current.status === 'failed') {
        sessionStorage.removeItem(storageKey);
        error =
          'Тема не прошла проверку. Исправьте указанные поля или файлы и загрузите ZIP снова.';
      }
    } catch (e) {
      if (!signal.aborted) error = errorText(e);
    } finally {
      busy = false;
      if (alive) onusagechange?.();
    }
  }
  async function save() {
    if (!upload || saving) return;
    saving = true;
    error = '';
    try {
      const version = await api<ThemeVersion>(
        target ? `/themes/${target}/versions` : '/themes',
        'POST',
        { upload_id: upload.id, ...(target ? { base_revision: base } : {}) }
      );
      sessionStorage.removeItem(storageKey);
      upload = null;
      file = null;
      if (alive) onsaved(version);
    } catch (e) {
      error =
        e instanceof APIError && e.code === 'conflict'
          ? 'Версия темы уже изменилась. Подготовленный ZIP сохранён. Обновите библиотеку и выберите актуальную базовую версию.'
          : errorText(e);
    } finally {
      saving = false;
    }
  }
</script>

<section class="panel stack theme-upload">
  <h2>Загрузить свою тему</h2>
  <p class="muted">
    ZIP с theme.json и вашими изображениями. До 5 МБ. Цвета и файлы пройдут проверку перед
    сохранением. Готовый результат загрузки хранится 30 дней после изменения статуса; сохраните его
    в оформление до окончания срока. <a href="/privacy">Правила хранения</a>.
  </p>
  <div class="row">
    <a class="text-link" href="/themes/example.zip" download>Скачать пример ZIP ↓</a><a
      class="text-link"
      href="/themes/guide.html"
      target="_blank"
      rel="noreferrer">Инструкция по формату ↗</a
    >
  </div>
  <label
    >ZIP темы<input
      type="file"
      accept=".zip,application/zip"
      disabled={busy || saving}
      onchange={(event) => {
        file = event.currentTarget.files?.[0] || null;
        upload = null;
        error = '';
      }}
    /></label
  >
  <button type="button" disabled={!file || busy || saving} onclick={start}
    >{busy ? 'Проверяем тему…' : 'Загрузить и проверить тему'}</button
  >
  {#if upload}<div role="status">
      <progress max={upload.size || 1} value={upload.offset}></progress>
      <p class="helper">
        {bytes(upload.offset)} из {bytes(upload.size)} · {upload.status === 'ready'
          ? 'Готова к сохранению'
          : upload.status === 'failed'
            ? 'Есть ошибки'
            : upload.status === 'uploading'
              ? 'Загрузка'
              : 'Подготовка изображений'}
      </p>
    </div>{/if}
  {#if upload?.issues?.length}<ul class="theme-issues">
      {#each upload.issues as issue}<li><strong>{issue.path}</strong>: {issue.message}</li>{/each}
    </ul>{/if}
  {#if upload?.status === 'ready' && upload.theme}<div class="notice">
      <strong>{upload.theme.manifest.name}</strong>
      <p class="helper">
        {upload.theme.manifest.author} · {bytes(upload.theme.size_bytes)} с архивом и обоими качествами
      </p>
    </div>
    <label
      >Сохранить как<select
        aria-label="Сохранить тему как"
        value={target}
        onchange={(event) => {
          target = event.currentTarget.value;
          base = cards.find((card) => card.id === target)?.revision || 0;
        }}
        ><option value="">Новую тему</option
        >{#each cards.filter((card) => !card.blocked) as card}<option value={card.id}
            >Новую версию: {card.name}</option
          >{/each}</select
      ></label
    >
    <p class="helper" data-testid="quota-theme-target-revision">
      {#if target}Версии выбранного оформления: {cards.find((card) => card.id === target)
          ?.revision ?? 'неизвестно'} из {usageLimits.theme_revisions}.
      {:else}Будет создано новое оформление с первой версией. Лимит — {usageLimits.theme_revisions} версий.{/if}
    </p>
    {#if target}<label
        >Базовая версия<input
          aria-label="Базовая версия темы"
          type="number"
          min="1"
          max={cards.find((card) => card.id === target)?.revision || base}
          bind:value={base}
        /></label
      >{/if}
    <button class="primary" type="button" disabled={saving} onclick={save}
      >{saving ? 'Сохраняем…' : 'Сохранить тему'}</button
    >{/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
</section>
