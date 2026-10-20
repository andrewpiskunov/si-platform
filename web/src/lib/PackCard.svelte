<script lang="ts">
  import type { Card } from './types';
  import { bytes, statusText } from './api';
  let { card, studio = false }: { card: Card; studio?: boolean } = $props();
</script>

<a class="pack-card" href={studio ? `/studio/${card.id}` : `/packs/${card.id}`}>
  <div class="card-symbol">
    <span aria-hidden="true">? / !</span><span class="pill"
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
  </div>
  <div class="pack-card-foot">
    <span>{card.question_count} вопросов · {card.round_count} раундов</span><span
      >{bytes(card.size_bytes)} ↗</span
    >
  </div>
</a>
