<script lang="ts">
  import AccountExport from '#lib/AccountExport.svelte';
  import UsagePanel from '#lib/UsagePanel.svelte';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, errorText } from '#lib/api.ts';
  import { user, refreshUser, authenticationChanged } from '#lib/session.ts';
  import type { Token } from '#lib/types.ts';
  let tokens = $state<Token[]>([]),
    error = $state(''),
    busy = $state(false),
    current = $state(''),
    password = $state(''),
    deletePassword = $state(''),
    deleteConfirmed = $state(false),
    name = $state(''),
    scopes = $state(['drafts:read']),
    issued = $state('');
  onMount(() => {
    void refreshUser();
    void loadTokens();
  });
  async function loadTokens() {
    try {
      tokens = (await api<{ tokens: Token[] | null }>('/account/tokens')).tokens || [];
    } catch (e) {
      error = errorText(e);
    }
  }
  async function logout() {
    busy = true;
    try {
      await api('/account/logout', 'POST');
      authenticationChanged();
      await goto('/login');
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function changePassword(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      await api('/account/password', 'POST', { current_password: current, new_password: password });
      authenticationChanged();
      current = '';
      password = '';
      await goto('/login');
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function remove(event: SubmitEvent) {
    event.preventDefault();
    if (!deleteConfirmed) return;
    busy = true;
    error = '';
    try {
      await api('/account/me', 'DELETE', { password: deletePassword });
      deletePassword = '';
      authenticationChanged();
      await goto('/');
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function createToken(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    issued = '';
    try {
      const result = await api<Token & { token: string }>('/account/tokens', 'POST', {
        name,
        scopes
      });
      issued = result.token;
      name = '';
      await loadTokens();
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
  async function revoke(id: string) {
    busy = true;
    try {
      await api(`/account/tokens/${id}`, 'DELETE');
      issued = '';
      await loadTokens();
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Аккаунт — Своя игра</title></svelte:head>
<section class="page">
  <div class="section-head row between">
    <div class="account-identity">
      <p class="eyebrow">Личный кабинет</p>
      <h1>{$user?.display_name || 'Аккаунт'}</h1>
      <p>{$user ? `@${$user.username}` : 'Войдите, чтобы управлять своим аккаунтом.'}</p>
    </div>
    {#if $user}<button onclick={logout} disabled={busy}>Выйти</button>{:else}<a
        class="button primary"
        href="/login">Войти</a
      >{/if}
  </div>
  {#if $user}<UsagePanel />{/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}{#if $user}<div
      class="two-columns"
    >
      <div class="stack">
        <div class="panel">
          <h2>Сменить пароль</h2>
          <p class="muted">После смены пароля потребуется войти снова.</p>
          <form class="stack" onsubmit={changePassword}>
            <label
              >Текущий пароль<input
                type="password"
                bind:value={current}
                required
                autocomplete="current-password"
              /></label
            ><label
              >Новый пароль<input
                type="password"
                bind:value={password}
                minlength="12"
                maxlength="128"
                required
                autocomplete="new-password"
              /><span class="helper">От 12 символов</span></label
            ><button disabled={busy}>Сохранить пароль</button>
          </form>
        </div>
        <AccountExport />
        <details>
          <summary>Удалить аккаунт</summary>
          <form class="stack" onsubmit={remove}>
            <p class="muted">
              Доступ к аккаунту и вашим публикациям будет закрыт, профиль будет обезличен.
              Сохранённые версии и файлы, привязки материалов в зависимых комнатах и журнал действий
              пока не удаляются физически. Сроки хранения и физическая очистка ещё не реализованы.
              Перед удалением скачайте свои данные.
            </p>
            <label
              >Подтвердите паролем<input
                type="password"
                bind:value={deletePassword}
                required
                autocomplete="current-password"
              /></label
            ><label class="check"
              ><input type="checkbox" bind:checked={deleteConfirmed} />Я понимаю, что удаление
              нельзя отменить.</label
            ><button class="danger" disabled={busy || !deleteConfirmed}>Удалить мой аккаунт</button>
          </form>
        </details>
      </div>
      <div class="panel">
        <h2>Токены доступа</h2>
        <p class="muted">
          Для подключения ваших инструментов. Доступ можно отозвать в любой момент.
        </p>
        <form class="stack" onsubmit={createToken}>
          <label
            >Название<input
              bind:value={name}
              placeholder="Мой редактор"
              required
              maxlength="64"
            /></label
          >
          <fieldset>
            <legend>Разрешения</legend>
            <div class="stack gap-sm">
              {#each [{ id: 'drafts:read', name: 'Чтение черновиков' }, { id: 'drafts:write', name: 'Редактирование черновиков' }, { id: 'drafts:export', name: 'Экспорт пакетов' }] as scope}<label
                  class="check"
                  ><input type="checkbox" bind:group={scopes} value={scope.id} />{scope.name}</label
                >{/each}
            </div>
          </fieldset>
          <button disabled={busy || !scopes.length}>Создать токен</button>
        </form>
        {#if issued}<div class="notice success" style="margin-top:20px">
            <p>Скопируйте токен. Он показывается только сейчас.</p>
            <code class="code-token">{issued}</code><button
              class="small"
              style="margin-top:12px"
              onclick={() => (issued = '')}>Скрыть токен</button
            >
          </div>{/if}
        <hr class="divider" />
        <div class="list">
          {#each tokens.filter((token) => !token.revoked_at) as token}<div class="stack gap-sm">
              <div class="row between">
                <strong>{token.name}</strong><button
                  class="small danger"
                  disabled={busy}
                  onclick={() => revoke(token.id)}>Отозвать</button
                >
              </div>
              <span class="helper"
                >До {new Date(token.expires_at).toLocaleDateString('ru-RU')} · {token.scopes.join(
                  ', '
                )}</span
              >
            </div>{:else}<p class="muted">Активных токенов нет.</p>{/each}
        </div>
      </div>
    </div>{/if}
</section>

<style>
  .account-identity {
    min-width: 0;
    max-width: 100%;
    overflow-wrap: anywhere;
  }
</style>
