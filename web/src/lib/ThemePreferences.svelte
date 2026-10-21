<script lang="ts">
  import { getContext } from 'svelte';
  import { roomThemeContext, type RoomTheme } from './room-theme.ts';
  const theme = getContext<RoomTheme>(roomThemeContext),
    detail = theme.detail;
  function update(key: 'classic' | 'economy', value: boolean) {
    const preferences = { ...$detail.preferences, [key]: value };
    try {
      sessionStorage.setItem(`appearance:${$detail.room}`, JSON.stringify(preferences));
    } catch {}
    theme.preferences(preferences);
  }
</script>

{#if $detail.room}<details class="theme-preferences">
    <summary>Оформление на моём экране</summary>
    <div class="row">
      <label class="check"
        ><input
          type="checkbox"
          checked={$detail.preferences.classic}
          onchange={(event) => update('classic', event.currentTarget.checked)}
        />Базовое оформление «Эфир»</label
      >
      <label class="check"
        ><input
          type="checkbox"
          checked={$detail.preferences.economy}
          onchange={(event) => update('economy', event.currentTarget.checked)}
        />Экономное качество оформления</label
      >
    </div>
    <p class="helper">Эти настройки действуют только в вашей вкладке для этой комнаты.</p>
  </details>{/if}
