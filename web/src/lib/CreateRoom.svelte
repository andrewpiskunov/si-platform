<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, errorText } from './api';
  import { user } from './session';
  import type { Credentials } from './types';
  import GameRules from './GameRules.svelte';
  import { classicRules, type Rules } from './game-v2.ts';
  let rules = $state<Rules>(classicRules());
  let {
    packId = '',
    revision = 0,
    disabled = false
  }: { packId?: string; revision?: number; disabled?: boolean } = $props();
  let host = $state(''),
    title = $state(''),
    theme = $state('classic'),
    busy = $state(false),
    error = $state('');
  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (disabled) return;
    busy = true;
    error = '';
    try {
      const credentials = await api<Credentials>('/rooms', 'POST', {
        host_name: host || $user?.display_name?.slice(0, 32) || 'Ведущий',
        pack_id: packId,
        revision,
        title,
        theme,
        rules
      });
      sessionStorage.setItem(
        `room:${credentials.room_id}`,
        JSON.stringify({
          room_id: credentials.room_id,
          participant_id: credentials.participant_id,
          token: credentials.token
        })
      );
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
  <div>
    <h2>Собираем компанию</h2>
    <p class="muted">Вы будете ведущим. До 12 игроков смогут присоединиться по ссылке.</p>
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
  <fieldset>
    <legend>Настроение вечера</legend>
    <div class="choices">
      {#each [{ id: 'classic', name: 'Классика' }, { id: 'pirate', name: 'Пираты' }, { id: 'anime', name: 'Аниме' }] as option}<button
          type="button"
          class:selected={theme === option.id}
          aria-pressed={theme === option.id}
          onclick={() => (theme = option.id)}
          ><span class={`swatch ${option.id}`} aria-hidden="true"></span>{option.name}</button
        >{/each}
    </div>
  </fieldset>
  <details>
    <summary>Правила и таймеры</summary><GameRules
      {rules}
      onchange={(value) => (rules = value)}
      disabled={busy}
    />
  </details>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if $user}<button
      class="primary"
      disabled={busy || disabled}>{busy ? 'Создаём комнату…' : 'Создать комнату →'}</button
    >{:else}<a class="button primary" href="/login">Войти и создать комнату</a>{/if}
</form>
