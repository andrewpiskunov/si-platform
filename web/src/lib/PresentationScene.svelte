<script lang="ts">
  import { questionTypes } from './game-v2.ts';
  let { stageID, kind, packName = '', roundName = '', themes = [], questionType = '', price = 0, theme = '', paused = false }: {
    stageID: string; kind: string; packName?: string; roundName?: string;
    themes?: { id: string; name: string }[]; questionType?: string; price?: number; theme?: string; paused?: boolean;
  } = $props();
  const hints: Record<string,string> = {
    stake: 'Повысьте ставку. Право ответа получит победитель торгов.',
    secret: 'Передайте вопрос другому игроку.',
    forAll: 'На этот вопрос отвечают все участники.',
    noRisk: 'За правильный ответ — удвоенная стоимость. За ошибку нет штрафа.',
    stakeAll: 'Сделайте закрытую ставку. Ответ определит, выиграете вы её или потеряете.'
  };
</script>

{#key stageID}
  <section class="presentation-scene" class:paused data-scene={kind} aria-label="Игровая заставка">
    {#if kind === 'round_intro'}
      <p class="scene-kicker">{packName || 'Своя игра'}</p>
      <h2>{roundName}</h2>
      <p class="scene-caption">Начинаем раунд</p>
    {:else if kind === 'round_themes'}
      <p class="scene-kicker">{roundName}</p>
      <h2 class="themes-heading">Темы раунда</h2>
      <ol>{#each themes as item, i (item.id)}<li><span>{String(i+1).padStart(2,'0')}</span>{item.name}</li>{/each}</ol>
    {:else if kind === 'question_price'}
      <p class="scene-kicker">{theme || (questionType === 'secret' ? 'Кот в мешке' : roundName)}</p>
      <h2 class="scene-price">{questionType === 'secret' ? '?' : price || 'Финал'}</h2>
      <p class="scene-caption">{questionType === 'secret' ? 'Сейчас узнаем, кому достанется вопрос' : price ? 'очков' : 'Решающая ставка'}</p>
    {:else if kind === 'question_type'}
      <p class="scene-kicker">Особый вопрос</p>
      <h2>{questionTypes[questionType] || 'Вопрос'}</h2>
      <p class="scene-caption">{hints[questionType] || ''}</p>
    {/if}
    {#if paused}<span class="scene-pause" role="status">Пауза</span>{/if}
  </section>
{/key}

<style>
  .presentation-scene {position:relative;flex:1;min-height:360px;display:flex;flex-direction:column;justify-content:center;align-items:center;gap:20px;text-align:center;padding:32px;background:var(--surface);border:2px solid var(--line);overflow:auto;}
  .scene-kicker {color:var(--accent);font-size:clamp(16px,1.8vw,26px);margin:0;}
  h2 {font-size:clamp(36px,5vw,88px);line-height:1.05;letter-spacing:-.03em;margin:0;overflow-wrap:anywhere;}
  h2.scene-price {font-size:clamp(80px,12vw,190px);color:var(--accent);font-variant-numeric:tabular-nums;}
  .scene-caption {font-size:clamp(16px,1.8vw,27px);max-width:700px;color:var(--muted);margin:0;text-wrap:balance;}
  .themes-heading {font-size:clamp(24px,3vw,44px);}
  ol {list-style:none;padding:0;margin:0;display:grid;gap:8px;width:min(800px,100%);}
  li {display:flex;gap:20px;text-align:left;font-size:clamp(18px,2vw,30px);padding:10px 18px;border-top:1px solid var(--line);overflow-wrap:anywhere;}
  li span {font-size:.65em;color:var(--accent);align-self:center;font-variant-numeric:tabular-nums;}
  .scene-pause {position:absolute;top:12px;right:12px;padding:6px 14px;background:var(--accent);color:var(--accent-text);}
  @media(min-width:1000px) and (min-height:600px) {.presentation-scene{min-height:0;}}
  @media(prefers-reduced-motion:no-preference) {
    .presentation-scene > :not(.scene-pause) {animation:scene-arrive .3s ease-out both;}
    .paused > * {animation-play-state:paused;}
    @keyframes scene-arrive {from {opacity:0;transform:translateY(10px)}to {opacity:1;transform:none}}
  }
</style>
