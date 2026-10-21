<script lang="ts">
  import { saveRoomCredentials } from './room-credentials.ts';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, errorText } from './api';
  import { user } from './session';
  import type { Credentials } from './types';
  import GameRules from './GameRules.svelte';
  import RoomSettingsFields from './RoomSettingsFields.svelte';
  import { normalizedSettings, validRoomSettings } from './room-preferences.ts';
  import { classicRules, type Rules } from './game-v2.ts';
  import ThemeChooser from './ThemeChooser.svelte';
  import type { Appearance } from './appearance.ts';
  let appearance = $state<Appearance>({ builtin: 'classic' });
  let rules = $state<Rules>({ ...classicRules(), auto_open: true, reading_seconds: 10 });
  let settings = $state({
    ...normalizedSettings(),
    require_ready: true
  } as import('./types.ts').RoomSettings);
  let mounted = $state(false);
  onMount(() => {
    mounted = true;
  });
  let {
    packId = '',
    revision = 0,
    disabled = false
  }: { packId?: string; revision?: number; disabled?: boolean } = $props();
  let host = $state(''),
    title = $state(''),
    busy = $state(false),
    selecting = $state(false),
    error = $state('');
  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (!mounted || busy || selecting || disabled || !validRoomSettings(settings)) return;
    busy = true;
    error = '';
    try {
      const credentials = await api<Credentials>('/rooms', 'POST', {
        host_name: host || $user?.display_name?.slice(0, 32) || 'Ведущий',
        pack_id: packId,
        revision,
        title,
        theme: appearance.builtin || 'classic',
        appearance,
        rules,
        settings
      });
      saveRoomCredentials(credentials);
      await goto(
        `/rooms/${credentials.room_id}${credentials.invite_token ? `#invite=${encodeURIComponent(credentials.invite_token)}` : ''}`
      );
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<form class="stack" onsubmit={create}>
  <fieldset class="stack" disabled={!mounted || busy || disabled}>
    <div>
      <h2>Собираем компанию</h2>
      <p class="muted">Вы будете ведущим. В комнате есть места для 12 игроков и 20 зрителей.</p>
    </div>
    <label
      >Название комнаты<input
        bind:value={title}
        placeholder="Наш игровой вечер"
        maxlength="80"
      /></label
    ><label
      >Ваше имя<input
        bind:value={host}
        placeholder={$user?.display_name || 'Ведущий'}
        maxlength="32"
      /></label
    >
    <ThemeChooser
      value={appearance}
      onchange={(value) => (appearance = value)}
      disabled={busy}
      onloading={(value) => (selecting = value)}
    />
    <RoomSettingsFields {settings} onchange={(value) => (settings = value)} disabled={busy} />
    <details>
      <summary>Правила и таймеры</summary><GameRules
        {rules}
        onchange={(value) => (rules = value)}
        disabled={busy}
      />
    </details>
    {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if $user}<button
        class="primary"
        disabled={busy || selecting || disabled || !validRoomSettings(settings)}
        >{busy ? 'Создаём комнату…' : 'Создать комнату →'}</button
      >{:else}<a class="button primary" href="/login">Войти и создать комнату</a>{/if}
  </fieldset>
</form>
