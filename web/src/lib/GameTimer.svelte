<script lang="ts">
  import { onMount } from 'svelte';
  import { timerSeconds, timerLabel, type Stage } from './game-v2.ts';
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
  const label = $derived(timerLabel(stage.kind));
  const seconds = $derived(timerSeconds(stage, serverTime, elapsed, paused));
</script>

{#if seconds !== null}<div
    class="game-timer"
    class:elapsed={seconds === 0}
    role="timer"
    aria-label={paused ? `Пауза: осталось ${seconds} секунд` : `${label}: ${seconds} секунд`}
  >
    <strong
      >{String(Math.floor(seconds / 60)).padStart(2, '0')}:{String(seconds % 60).padStart(
        2,
        '0'
      )}</strong
    ><span>{paused ? 'Пауза' : stage.expired ? 'Время вышло' : label}</span>
  </div>{/if}

<style>
  .game-timer {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px;
    border: 1px solid var(--accent, #f2d78c);
    border-radius: 8px;
  }
  .game-timer strong {
    font:
      600 32px/1.1 Arial,
      sans-serif;
    font-variant-numeric: tabular-nums;
    color: var(--accent, #f2d78c);
  }
  .game-timer span {
    font-size: 14px;
  }
</style>
