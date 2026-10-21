<script lang="ts">
  import type { Question, Draft } from './types.ts';
  import { findPrepared } from './media-model.ts';
  import PreparedPreview from './PreparedPreview.svelte';
  let {
    question,
    assets,
    packId,
    revision = 0
  }: { question: Question; assets: Draft['assets']; packId: string; revision?: number } = $props();
  let quality = $state<'standard' | 'economy'>('standard');
  let selected = $state('');
</script>

<label
  >Качество предпросмотра<select bind:value={quality}
    ><option value="standard">Обычное</option><option value="economy">Экономное</option></select
  ></label
>
{#each ['question', 'answer'] as group}<div class="question-stage">
    <p class="question-price">
      {group === 'question' ? `${question.price} очков` : 'Пояснение к ответу'}
    </p>
    {#each question.params.find((p) => p.name === group)?.items || [] as content, index}{#if content.type === 'text'}<h2
        >
          {content.value || 'Текст появится здесь'}
        </h2>{:else}{@const asset = findPrepared(
          assets,
          content.type,
          content.value
        )}{#if asset}<button class="small" onclick={() => (selected = `${group}:${index}`)}
            >Показать {group === 'question' ? 'медиа вопроса' : 'медиа ответа'} · {index +
              1}</button
          >{#if selected === `${group}:${index}`}<PreparedPreview
              {asset}
              {packId}
              {revision}
              {quality}
            />{/if}{:else}<p class="helper">
            {content.type}: {content.value || 'Файл не выбран'} · нет подготовленной версии
          </p>{/if}{/if}{/each}
  </div>{/each}
<div class="notice" style="margin-top:16px">
  <strong>Ответ ведущего:</strong>
  {question.right.filter(Boolean).join(' / ') || 'Не заполнен'}
</div>
