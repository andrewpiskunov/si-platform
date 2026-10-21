<script lang="ts">
  import { untrack } from 'svelte';
  import { api, errorText, APIError } from './api.ts';
  import { appearanceKey, type Appearance } from './appearance.ts';
  import ThemeChooser from './ThemeChooser.svelte';
  let {
    room,
    token,
    seq,
    phase,
    appearance,
    ready,
    onapplied
  }: {
    room: string;
    token: string;
    seq: number;
    phase: string;
    appearance?: Appearance;
    ready: boolean;
    onapplied: () => void;
  } = $props();
  let selected = $state<Appearance>({ builtin: 'classic' }),
    error = $state(''),
    busy = $state(false),
    selecting = $state(false);
  let pending = $state<{ appearance: Appearance; command_id: string; expected_seq: number } | null>(
    null
  );
  const key = $derived(appearanceKey(appearance));
  $effect(() => {
    void key;
    untrack(() => {
      selected = appearance || { builtin: 'classic' };
      pending = null;
    });
  });
  async function apply() {
    if (busy || selecting || !ready) return;
    busy = true;
    error = '';
    pending ||= { appearance: selected, command_id: crypto.randomUUID(), expected_seq: seq };
    try {
      await api(`/rooms/${room}/appearance`, 'POST', pending, token);
      pending = null;
      onapplied();
    } catch (e) {
      error = errorText(e);
      if (e instanceof APIError && !['unavailable', 'ownership_lost'].includes(e.code))
        pending = null;
    } finally {
      busy = false;
    }
  }
</script>

<details class="theme-apply">
  <summary>Оформление комнаты</summary>{#if phase === 'lobby' || phase === 'paused'}<div
      class="stack"
    >
      {#if phase === 'paused'}<p class="notice">
          Смена оформления коснётся всех участников. Игра останется на паузе.
        </p>{/if}
      <ThemeChooser
        value={selected}
        onchange={(value) => {
          selected = value;
          pending = null;
        }}
        disabled={busy}
        onloading={(value) => (selecting = value)}
      />
      <button disabled={busy || selecting || !ready} onclick={apply}
        >{busy ? 'Применяем…' : pending ? 'Повторить применение' : 'Применить оформление'}</button
      >
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
    </div>{:else}<p class="helper">Оформление можно изменить в лобби или на паузе.</p>{/if}
</details>
