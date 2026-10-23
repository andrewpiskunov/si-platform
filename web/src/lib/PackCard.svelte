<script lang="ts">
  import Icon from './Icon.svelte';
 import PackEngagement from './PackEngagement.svelte';
 import PackCover from './PackCover.svelte';
 import {sourceDate} from './source-metadata.ts';
  import { page } from '$app/state';
  import { contextLink, roomContext } from './appearance.ts';
  import type { Card } from './types';
  import { bytes, statusText } from './api';
  let { card, studio = false }: { card: Card; studio?: boolean } = $props();
</script>

<a
  class="pack-card"
  href={contextLink(studio ? `/studio/${card.id}` : `/packs/${card.id}`, roomContext(page.url))}
>
  <div class="card-symbol">
    <PackCover src={card.cover_url} name={card.name} /><span
      class="pill"
      >{studio
        ? statusText[card.status] || card.status
        : card.has_media
          ? 'С медиа'
          : 'Текстовые вопросы'}</span
    >
  </div>
  <div class="pack-card-body">
    <div class="row gap-sm">
      {#each (card.tags || []).slice(0, 3) as tag}<span class="tag">{tag}</span>{/each}<span
        class="tag">{card.language || 'ru'}</span
      >
    </div>
    <h3>{card.name || 'Новый пакет'}</h3>
    <span class="byline">{card.author || 'Автор пакета'}</span>
 {#if card.source}<small class="muted">{sourceDate(card.source.published_date)}</small>{/if}
 {#if !studio && card.engagement}<PackEngagement packId={card.id} initial={card.engagement} compact />{/if}
  </div>
  <div class="pack-card-foot">
    <span>{card.question_count} вопросов · {card.round_count} раундов</span><span
      >{bytes(card.size_bytes)} <Icon name="arrow" /></span
    >
  </div>
</a>
