<script lang="ts">
  import { onMount } from 'svelte';
  import type { RevealView } from './game-v2.ts';
  import { revealProgress, revealText } from './reveal-model.ts';
  let {
    text,
    reveal,
    serverTime = 0,
    paused = false
  }: {
    text: string;
    reveal?: RevealView;
    serverTime?: number;
    paused?: boolean;
  } = $props();
  let elapsed = $state(0),
    reducedMotion = $state(false);
  $effect.pre(() => {
    void serverTime;
    elapsed = 0;
    if (!reveal || paused) return;
    const started = performance.now();
    const timer = setInterval(() => {
      elapsed = performance.now() - started;
    }, 50);
    return () => clearInterval(timer);
  });
  onMount(() => {
    const preference = matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => {
      reducedMotion = preference.matches;
    };
    update();
    preference.addEventListener('change', update);
    return () => preference.removeEventListener('change', update);
  });
  const progress = $derived(
    revealProgress(reveal, serverTime, serverTime + elapsed, paused, reducedMotion)
  );
  const content = $derived(revealText(text, reveal, progress));
  // Fit the actual rendered text, including the selected room font. At the
  // readable lower bound the container scrolls rather than truncating content.
  function fit(node: HTMLHeadingElement, _text: string) {
    const area = node.parentElement!;
    let frame = 0,
      alive = true;
    function measure() {
      if (!alive || !area.clientHeight || !area.clientWidth) return;
      let low = 20,
        high = Math.min(72, Math.max(32, innerWidth * 0.045));
      for (let i = 0; i < 8; i++) {
        const size = (low + high) / 2;
        node.style.fontSize = `${size}px`;
        if (node.scrollHeight <= area.clientHeight && node.scrollWidth <= area.clientWidth)
          low = size;
        else high = size;
      }
      node.style.fontSize = `${Math.floor(low)}px`;
    }
    function schedule() {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(measure);
    }
    const observer = new ResizeObserver(schedule);
    observer.observe(area);
    void document.fonts.ready.then(() => {
      if (alive) schedule();
    });
    schedule();
    return {
      update: schedule,
      destroy() {
        alive = false;
        observer.disconnect();
        cancelAnimationFrame(frame);
      }
    };
  }
</script>

<div class="adaptive-question-text" data-testid="question-reveal" data-progress={progress}>
  <h2 use:fit={text} aria-busy={!!content.concealed}>
    <span class="question-visible">{content.visible}</span><span
      class="question-concealed"
      aria-hidden="true">{content.concealed}</span
    >
  </h2>
</div>

<style>
  .adaptive-question-text {
    flex: 1;
    min-height: 0;
    width: 100%;
    display: grid;
    align-items: safe center;
    overflow: auto;
  }
  .question-concealed {
    visibility: hidden;
    user-select: none;
  }
  h2 {
    margin: 0 !important;
    width: 100%;
    font-size: 32px;
    line-height: 1.25;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-weight: 550;
    text-align: center;
  }
  @media (max-width: 999px), (max-height: 599px) {
    .adaptive-question-text {
      min-height: 220px;
      max-height: 60dvh;
    }
  }
</style>
