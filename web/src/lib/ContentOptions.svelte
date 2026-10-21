<script lang="ts">
  import type { Content } from './types.ts';
  let { item, onchange }: { item: Content; onchange: (patch: Partial<Content>) => void } = $props();
</script>

<details>
  <summary>Показ и длительность фрагмента</summary>
  <div class="stack">
    <label
      >Кому показывать<select
        value={item.placement || ''}
        onchange={(event) => onchange({ placement: event.currentTarget.value })}
        ><option value="">По умолчанию — всем</option><option value="screen"
          >На экране у всех</option
        ><option value="replic">Только ведущему</option
        >{#if item.placement && !['screen', 'replic'].includes(item.placement)}<option
            value={item.placement}>{item.placement} · импортированное значение</option
          >{/if}</select
      ></label
    ><label
      >Длительность, мс<input
        type="number"
        min="0"
        max="600000"
        step="1"
        value={item.duration_ms ?? 0}
        onchange={(event) => onchange({ duration_ms: Number(event.currentTarget.value) })}
      /><span class="helper"
        >{['audio', 'video'].includes(item.type)
          ? '0 — до конца файла с автоматическим переходом. Ручное завершение ниже отключает переход, но не ограничение воспроизведения.'
          : '0 — без автоматического перехода.'}</span
      ></label
    ><label class="check"
      ><input
        type="checkbox"
        checked={item.wait_for_finish || false}
        onchange={(event) => onchange({ wait_for_finish: event.currentTarget.checked })}
      />Ведущий завершает фрагмент вручную</label
    >
  </div>
</details>
