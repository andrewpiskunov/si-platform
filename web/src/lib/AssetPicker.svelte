<script lang="ts">
  import type { Asset, Content } from './types.ts';
  import { readyAssets, assetReference, findPrepared } from './media-model.ts';
  let {
    assets,
    item,
    label,
    onchange
  }: {
    assets: Asset[];
    item: Content;
    label: string;
    onchange: (patch: Partial<Content>) => void;
  } = $props();
  const available = $derived(readyAssets(assets, item.type));
  const selected = $derived(findPrepared(assets, item.type, item.value));
</script>

<label
  >{label}<select
    aria-label={label}
    value={selected ? assetReference(selected) : item.value}
    onchange={(event) => onchange({ value: event.currentTarget.value, is_ref: true })}
    ><option value="">Выберите готовый файл</option>{#if item.value && !selected}<option
        value={item.value}
        disabled>{item.value} · не подготовлен</option
      >{/if}{#each available as asset}<option value={assetReference(asset)}>{asset.name}</option
      >{/each}</select
  ><span class="helper"
    >{available.length
      ? 'Доступны проверенные версии файлов этого типа.'
      : 'Сначала загрузите и подготовьте файл в разделе «Медиафайлы».'}</span
  ></label
>
