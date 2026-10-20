<script lang="ts">
  import type { Question, Draft } from './types';
  let {
    question,
    assets,
    packId
  }: { question: Question; assets: Draft['assets']; packId: string } = $props();
  function source(value: string) {
    const asset = assets.find((item) => item.name === value);
    return asset ? `/api/drafts/${encodeURIComponent(packId)}/assets/${asset.key}` : '';
  }
</script>

<div class="question-stage">
  <div class="question-price">{question.price} очков</div>
  {#each question.params.find((parameter) => parameter.name === 'question')?.items || [] as content}{#if content.type === 'text'}<h2
      >
        {content.value || 'Текст вопроса появится здесь'}
      </h2>{:else if content.is_ref && source(content.value)}{#if content.type === 'image'}<img
          src={source(content.value)}
          alt="Иллюстрация к вопросу"
          style="max-width:100%;max-height:360px"
        />{:else if content.type === 'audio'}<audio controls src={source(content.value)}
        ></audio>{:else if content.type === 'video'}<video
          controls
          src={source(content.value)}
          style="max-width:100%"><track kind="captions" /></video
        >{/if}{:else}<p class="helper">{content.type}: {content.value}</p>{/if}{/each}
</div>
<div class="notice" style="margin-top:16px">
  <strong>Ответ ведущего:</strong>
  {question.right.filter(Boolean).join(' / ') || 'Не заполнен'}
</div>
