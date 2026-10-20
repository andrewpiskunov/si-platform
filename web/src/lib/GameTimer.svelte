<script lang="ts">
  import { onMount } from 'svelte';
  import { timerSeconds, type Stage } from './game-v2.ts';
  let {
    stage,
    serverTime,
    paused = false
  }: { stage: Stage; serverTime: number; paused?: boolean } = $props();
  let elapsed = $state(0);
  let anchor = 0;
  $effect(() => {
    void serverTime;
    void stage.id;
    void paused;
    anchor = performance.now();
    elapsed = 0;
  });
  onMount(() => {
    const timer = setInterval(() => (elapsed = performance.now() - anchor), 200);
    return () => clearInterval(timer);
  });
  const seconds = $derived(timerSeconds(stage, serverTime, elapsed, paused));
</script>

{#if seconds !== null}<div
    class="game-timer"
    class:elapsed={seconds === 0}
    role="timer"
    aria-label={paused ? `Пауза: осталось ${seconds} секунд` : `Осталось ${seconds} секунд`}
  >
    <strong
      >{String(Math.floor(seconds / 60)).padStart(2, '0')}:{String(seconds % 60).padStart(
        2,
        '0'
      )}</strong
    ><span>{paused ? 'Пауза' : stage.expired ? 'Время вышло' : 'Осталось'}</span>
  </div>{/if}
