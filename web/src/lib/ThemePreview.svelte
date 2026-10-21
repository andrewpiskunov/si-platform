<script lang="ts">
  import { untrack } from 'svelte';
  import {
    AppearanceResources,
    manifestStyle,
    fetchThemeAssets,
    type ThemeVersion
  } from './appearance.ts';
  import { bytes } from './api.ts';
  let { version }: { version: ThemeVersion } = $props();
  let quality = $state<'standard' | 'economy'>('standard'),
    size = $state('desktop'),
    images = $state<Record<string, string>>({}),
    error = $state(''),
    style = $state('');
  const resources = new AppearanceResources();
  const key = $derived(`${version.card.id}:${version.card.revision}:${quality}`);
  $effect(() => {
    void key;
    return untrack(() => {
      const request = resources.begin(),
        current = version,
        chosen = quality;
      images = {};
      error = '';
      try {
        style = manifestStyle(current.prepared.manifest);
      } catch {
        error = 'Некорректное оформление.';
        style = '';
      }
      void load(current, chosen, request);
      return () => resources.clear();
    });
  });
  async function load(
    current: ThemeVersion,
    chosen: 'standard' | 'economy',
    request: AbortController
  ) {
    try {
      const assets = (current.prepared.assets || []).map((asset, index) => ({
        index,
        name: asset.name,
        width: asset.prepared.width,
        height: asset.prepared.height,
        standard_size: asset.prepared.standard.size,
        economy_size: asset.prepared.economy.size
      }));
      const result = await fetchThemeAssets(
        assets,
        chosen,
        current.prepared.manifest,
        (index) =>
          `/api/themes/${current.card.id}/assets/${index}?revision=${current.card.revision}&quality=${chosen}`,
        AbortSignal.any([request.signal, AbortSignal.timeout(5000)])
      );
      if (resources.commit(request, Object.values(result))) images = result;
    } catch {
      if (resources.current(request)) error = 'Не удалось загрузить предпросмотр изображений.';
    }
  }
  const manifest = $derived(version.prepared.manifest);
</script>

<section class="stack">
  <div class="row between">
    <h2>Предпросмотр темы</h2>
    <div class="row">
      <label
        >Размер экрана<select aria-label="Размер предпросмотра темы" bind:value={size}
          ><option value="desktop">Компьютер</option><option value="mobile">Телефон</option></select
        ></label
      ><label
        >Качество оформления<select aria-label="Качество предпросмотра темы" bind:value={quality}
          ><option value="standard">Обычное</option><option value="economy">Экономное</option
          ></select
        ></label
      >
    </div>
  </div>
  <p class="helper">
    Архив: {bytes(version.prepared.original_size)} · Обычное: {bytes(
      (version.prepared.assets || []).reduce((sum, asset) => sum + asset.prepared.standard.size, 0)
    )} · Экономное: {bytes(
      (version.prepared.assets || []).reduce((sum, asset) => sum + asset.prepared.economy.size, 0)
    )}
  </p>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <div
    class="theme-preview"
    class:mobile={size === 'mobile'}
    data-theme="custom"
    {style}
    aria-label="Предпросмотр оформления"
  >
    {#if manifest.background && images[manifest.background]}<img
        class="theme-background"
        src={images[manifest.background]}
        alt=""
      />{/if}{#if manifest.ornament && images[manifest.ornament]}<img
        class="theme-ornament"
        src={images[manifest.ornament]}
        alt=""
      />{/if}
    <div class="theme-preview-content">
      <div class="row">
        {#if manifest.logo && images[manifest.logo]}<img
            class="theme-logo"
            src={images[manifest.logo]}
            alt=""
          />{/if}<strong>{manifest.name}</strong>
      </div>
      <div class="panel">
        <p class="eyebrow">Пример вопроса · 300 очков</p>
        <h2>Какой вопрос вы зададите друзьям?</h2>
        <p class="muted">Так выглядят текст и элементы игры в этой теме.</p>
        <button type="button" class="buzz">Знаю ответ!</button>
      </div>
      <div class="theme-preview-players">
        {#each Array.from({ length: 12 }, (_, i) => i + 1) as index}<div class="panel">
            <span>Игрок {index}</span><strong>{index * 100}</strong>
          </div>{/each}
      </div>
      <label>Пример поля<input placeholder="Ваш ответ" /></label>
      <p class="notice error">Пример сообщения об ошибке</p>
    </div>
  </div>
  <p class="helper">
    Это предпросмотр оформления. Игровые правила и размеры кнопки задаёт платформа.
  </p>
</section>
