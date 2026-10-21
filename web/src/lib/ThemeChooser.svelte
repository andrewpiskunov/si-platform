<script lang="ts">
  import { onMount } from 'svelte';
  import { user } from './session.ts';
  import { api, errorText } from './api.ts';
  import type { Appearance, ThemeCard, ThemeVersion } from './appearance.ts';
  let {
    value,
    onchange,
    disabled = false
  }: { value: Appearance; onchange: (value: Appearance) => void; disabled?: boolean } = $props();
  let cards = $state<ThemeCard[]>([]),
    error = $state(''),
    busy = $state(false),
    mounted = $state(false);
  const userID = $derived($user?.id || '');
  $effect(() => {
    if (mounted && userID) void load();
  });
  onMount(() => {
    mounted = true;
  });
  async function load() {
    try {
      cards = (await api<{ themes: ThemeCard[] }>('/themes')).themes || [];
    } catch {
      cards = [];
    }
  }
  async function custom(id: string) {
    if (!id) return;
    busy = true;
    error = '';
    try {
      const version = await api<ThemeVersion>(`/themes/${id}`);
      onchange({ theme_id: id, revision: version.card.revision, hash: version.prepared.hash });
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<fieldset disabled={disabled || busy}>
  <legend>Настроение вечера</legend>
  <div class="choices">
    {#each [{ id: 'classic', name: 'Классика' }, { id: 'pirate', name: 'Пираты' }, { id: 'anime', name: 'Аниме' }] as option}<button
        type="button"
        class:selected={value.builtin === option.id}
        aria-pressed={value.builtin === option.id}
        onclick={() => onchange({ builtin: option.id as 'classic' | 'pirate' | 'anime' })}
        ><span class={`swatch ${option.id}`} aria-hidden="true"></span>{option.name}</button
      >{/each}
  </div>
  {#if $user}<label
      >Моя тема<select
        aria-label="Моя тема"
        value={value.theme_id || ''}
        onchange={(event) => void custom(event.currentTarget.value)}
        ><option value="">Выберите из библиотеки</option>{#each cards as card}<option
            value={card.id}
            disabled={card.blocked}
            >{card.name} · версия {card.revision}{card.blocked ? ' · заблокирована' : ''}</option
          >{/each}</select
      ></label
    >{:else}<p class="helper">Свои темы доступны после входа в аккаунт автора.</p>{/if}
  {#if value.theme_id}<p class="helper">
      Выбрана версия {value.revision}. Новые версии не меняют эту комнату автоматически.
    </p>{/if}
</fieldset>
{#if error}<p class="notice error" role="alert">{error}</p>{/if}
