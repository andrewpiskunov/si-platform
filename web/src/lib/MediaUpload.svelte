<script lang="ts">
  import { onMount } from 'svelte';
  import { api, APIError, errorText, bytes } from './api.ts';
  import type { Upload } from './types.ts';
  let {
    packId,
    disabled = false,
    onflush,
    onattach
  }: {
    packId: string;
    disabled?: boolean;
    onflush: () => Promise<void>;
    onattach: (id: string, signal: AbortSignal) => Promise<void>;
  } = $props();
  let file = $state<File | null>(null),
    kind = $state('image'),
    upload = $state<Upload | null>(null),
    busy = $state(false),
    error = $state(''),
    message = $state('');
  let controller: AbortController | undefined,
    alive = true;
  onMount(() => () => {
    alive = false;
    controller?.abort();
  });
  function select(event: Event) {
    file = (event.currentTarget as HTMLInputElement).files?.[0] || null;
    upload = null;
    error = '';
    message = '';
    if (file?.type.startsWith('audio/')) kind = 'audio';
    else if (file?.type.startsWith('video/')) kind = 'video';
    else kind = 'image';
  }
  async function start() {
    if (!file || busy) return;
    const selected = file;
    if (selected.size === 0 || selected.size > 100 * 1024 * 1024) {
      error = 'Размер файла должен быть от 1 байта до 100 МБ.';
      return;
    }
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    busy = true;
    error = '';
    message = '';
    const key = `media-upload:${packId}:${kind}:${selected.name}:${selected.size}:${selected.lastModified}`;
    try {
      await onflush();
      if (signal.aborted) return;
      let current: Upload;
      const previous = sessionStorage.getItem(key);
      try {
        current = previous
          ? await api<Upload>(`/uploads/${previous}`, 'GET', undefined, undefined, signal)
          : await api<Upload>(
              '/media-uploads',
              'POST',
              { name: selected.name, size: selected.size, kind },
              undefined,
              signal
            );
      } catch (e) {
        if (e instanceof APIError && [404, 410].includes(e.status))
          current = await api<Upload>(
            '/media-uploads',
            'POST',
            { name: selected.name, size: selected.size, kind },
            undefined,
            signal
          );
        else throw e;
      }
      sessionStorage.setItem(key, current.id);
      upload = current;
      while (!signal.aborted && current.status === 'uploading' && current.offset < selected.size) {
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
        if (!response.ok) {
          if (response.status === 409) {
            current = await api<Upload>(
              `/uploads/${current.id}`,
              'GET',
              undefined,
              undefined,
              signal
            );
            upload = current;
            continue;
          }
          const data = await response.json().catch(() => ({ error: 'unavailable' }));
          throw new APIError(data.error, response.status);
        }
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
          const done = () => {
            signal.removeEventListener('abort', abort);
            resolve();
          };
          const timer = setTimeout(done, 1000);
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
        sessionStorage.removeItem(key);
        throw new Error(current.error || 'media_invalid');
      }
      message = 'Файл подготовлен. Прикрепляем к текущей версии…';
      await onattach(current.id, signal);
      sessionStorage.removeItem(key);
      message = 'Медиафайл прикреплён. Его можно выбрать в вопросе или пояснении.';
    } catch (e) {
      if (!signal.aborted && alive) error = errorText(e);
    } finally {
      if (alive) busy = false;
    }
  }
</script>

<div class="stack media-upload">
  <h3>Добавить медиафайл</h3>
  <label
    >Медиафайл<input
      type="file"
      disabled={busy || disabled}
      accept="image/png,image/jpeg,image/webp,audio/*,video/*"
      onchange={select}
    /></label
  ><label
    >Тип медиа<select bind:value={kind} disabled={busy || disabled}
      ><option value="image">Изображение</option><option value="audio">Аудио</option><option
        value="video">Видео</option
      ></select
    ></label
  >
  <p class="helper">
    До 100 МБ, аудио и видео — до 10 минут. Файл проверяется и готовится в двух вариантах качества.
  </p>
  <button class="primary" disabled={!file || busy || disabled} onclick={start}
    >{busy
      ? 'Подготовка медиа…'
      : upload?.status === 'ready'
        ? 'Прикрепить готовый файл'
        : 'Загрузить и прикрепить'}</button
  >{#if upload}<div role="status">
      <progress class="progress" max={upload.size || 1} value={upload.offset}></progress><span
        class="helper"
        >{bytes(upload.offset)} из {bytes(upload.size)} · {upload.status === 'uploading'
          ? 'Загрузка'
          : upload.status === 'ready'
            ? 'Готово'
            : upload.status === 'failed'
              ? 'Не удалось подготовить'
              : 'Проверка и подготовка'}</span
      >
    </div>{/if}{#if message}<p class="notice success" role="status">{message}</p>{/if}{#if error}<p
      class="notice error"
      role="alert"
    >
      {error}
    </p>{/if}
</div>
