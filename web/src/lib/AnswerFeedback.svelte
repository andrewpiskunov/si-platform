<script lang="ts">
  import {questionFeedback} from './game-feedback.ts';
  import type {SessionView} from './game-v2.ts';
  import type {Participant} from './room-client.ts';
  let {view,participants}: {view:SessionView;participants:Participant[]} = $props();
  const feedback=$derived(questionFeedback(view));
  const name=$derived(participants.find(p=>p.id===feedback?.participantID)?.name || 'Участник');
  const labels={answering:'Отвечает',judging:'Оценка ответа',correct:'Верно',incorrect:'Неверно',correction:'Решение исправлено'};
</script>
{#if feedback}
  {#key feedback.key}
    <div class="answer-feedback" class:answering={feedback.kind==='answering'} class:incorrect={feedback.kind==='incorrect'} role="status" aria-live="polite">
      <strong>{labels[feedback.kind]} · {name}</strong>
      {#if feedback.decision}<span>{feedback.decision.delta>0?'+':''}{feedback.decision.delta} очков</span>{/if}
    </div>
  {/key}
{/if}
<style>
  .answer-feedback{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:10px 16px;border:1px solid var(--line);background:var(--surface);color:var(--text);font-size:clamp(17px,1.8vw,25px);flex:none;overflow-wrap:anywhere;}
  .answering{border-color:var(--accent);color:var(--accent);}
  .answer-feedback span{white-space:nowrap;color:var(--accent);font-variant-numeric:tabular-nums;}
  .incorrect span{color:var(--danger);}
  @media(prefers-reduced-motion:no-preference){.answer-feedback{animation:feedback-in .25s ease-out;}@keyframes feedback-in{from{opacity:.3;transform:translateY(4px)}to{opacity:1;transform:none}}}
</style>
