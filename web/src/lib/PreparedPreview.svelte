<script lang="ts">
  import type { Asset } from './types.ts';
  import { preparedURL } from './media-model.ts';
  let {
    asset,
    packId,
    revision,
    quality = 'standard'
  }: {
    asset: Asset;
    packId: string;
    revision: number;
    quality?: 'standard' | 'economy';
  } = $props();
  const source = $derived(
    asset.prepared ? preparedURL(packId, revision, asset.prepared[quality].key) : ''
  );
</script>

{#if source && asset.prepared}<div class="prepared-preview">
    {#if asset.prepared.kind === 'image'}<img
        src={source}
        alt={`Предпросмотр ${asset.name}`}
        loading="lazy"
      />{:else if asset.prepared.kind === 'audio'}<audio src={source} controls preload="metadata"
      ></audio>{:else}<video src={source} controls preload="metadata"
        ><track kind="captions" /></video
      >{/if}
  </div>{/if}
