<script lang="ts">
  import type { Asset } from './types.ts';
  import { bytes } from './api.ts';
  import PreparedPreview from './PreparedPreview.svelte';
  let { assets, packId, revision }: { assets: Asset[]; packId: string; revision: number } =
    $props();
  let quality = $state<'standard' | 'economy'>('standard');
  let selected = $state('');
</script>

<section class="stack media-library">
  <h2>Медиафайлы</h2>
  <label
    >Качество предпросмотра<select bind:value={quality}
      ><option value="standard">Обычное</option><option value="economy">Экономное</option></select
    ></label
  >{#each assets as asset (asset.name)}<details
      ontoggle={(event) => {
        if (event.currentTarget.open) selected = asset.name;
        else if (selected === asset.name) selected = '';
      }}
    >
      <summary>{asset.name} · {asset.prepared ? 'Готово' : 'Не подготовлен'}</summary>
      <div class="stack">
        <p class="helper">
          Оригинал: {bytes(asset.size)}{#if asset.prepared}<br />Обычное: {bytes(
              asset.prepared.standard.size
            )} · Экономное: {bytes(asset.prepared.economy.size)}{#if asset.prepared.duration_ms}<br
              />Длительность: {(asset.prepared.duration_ms / 1000).toFixed(1)} с{/if}{#if asset.prepared.width}<br
              />{asset.prepared.width} × {asset.prepared.height} px{/if}{/if}
        </p>
        {#if asset.prepared}{#if selected === asset.name}<PreparedPreview
              {asset}
              {packId}
              {revision}
              {quality}
            />{/if}{:else}<p class="notice">
            Для игры нужна подготовленная версия файла. Подробности — в диагностике пакета. Исходник
            сохраняется для экспорта SIQ.
          </p>{/if}<a class="text-link" href={`/api/drafts/${packId}/assets/${asset.key}`} download
          >Скачать оригинал</a
        >
      </div>
    </details>{:else}<p class="helper">
      Загрузите файл, затем выберите его в нужном фрагменте вопроса или пояснения.
    </p>{/each}
</section>
