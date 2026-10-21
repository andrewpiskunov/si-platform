<script lang="ts">
  let {
    label,
    index,
    count,
    onmove
  }: {
    label: string;
    index: number;
    count: number;
    onmove: (delta: -1 | 1) => void;
  } = $props();
</script>

<div class="reorder-controls" role="group" aria-label={`Порядок: ${label}`}>
  <button
    type="button"
    class="small"
    disabled={index <= 0 || index >= count}
    aria-label={`${label}: выше`}
    onclick={() => onmove(-1)}><span aria-hidden="true">↑</span> Выше</button
  >
  <button
    type="button"
    class="small"
    disabled={index < 0 || index >= count - 1}
    aria-label={`${label}: ниже`}
    onclick={() => onmove(1)}><span aria-hidden="true">↓</span> Ниже</button
  >
</div>

<style>
  .reorder-controls {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  button {
    min-height: 32px;
    white-space: nowrap;
  }
</style>
