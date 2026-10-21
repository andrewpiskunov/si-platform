<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, errorText } from '#lib/api.ts';
  import { refreshUser, authenticationChanged } from '#lib/session.ts';
  let register = $state(false),
    username = $state(''),
    password = $state(''),
    displayName = $state(''),
    busy = $state(false),
    error = $state('');
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      await api(
        `/account/${register ? 'register' : 'login'}`,
        'POST',
        register ? { username, password, display_name: displayName } : { username, password }
      );
      password = '';
      authenticationChanged();
      await refreshUser();
      await goto('/studio');
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Войти — Своя игра</title></svelte:head>
<section class="page narrow">
  <p class="eyebrow">Рады видеть вас за столом</p>
  <h1>Ваша игра.<br />Ваш аккаунт.</h1>
  <p class="muted">Сохраняйте пакеты и собирайте друзей в своей комнате.</p>
  <div class="panel auth-card">
    <div class="auth-tabs">
      <button
        class:primary={!register}
        onclick={() => {
          register = false;
          error = '';
        }}>Вход</button
      ><button
        class:primary={register}
        onclick={() => {
          register = true;
          error = '';
        }}>Регистрация</button
      >
    </div>
    <form class="stack" onsubmit={submit}>
      <label
        >Логин<input
          bind:value={username}
          autocomplete="username"
          required
          minlength="3"
          maxlength="32"
          pattern="[a-zA-Z0-9_]+"
          placeholder="your_name"
        /><span class="helper">3–32 латинских буквы, цифры или подчёркивание</span></label
      >{#if register}<label
          >Имя в игре<input
            bind:value={displayName}
            autocomplete="nickname"
            required
            maxlength="32"
            placeholder="Как вас зовут?"
          /></label
        >{/if}<label
        >Пароль<input
          type="password"
          bind:value={password}
          autocomplete={register ? 'new-password' : 'current-password'}
          required
          minlength={register ? 12 : 1}
          maxlength="128"
        />{#if register}<span class="helper">От 12 символов</span>{/if}</label
      >{#if error}<div class="notice error" role="alert">{error}</div>{/if}<button
        class="primary"
        disabled={busy}>{busy ? 'Подождите…' : register ? 'Создать аккаунт' : 'Войти'}</button
      >
    </form>
  </div>
  <p class="helper" style="margin-top:20px">
    Чтобы присоединиться к игре по приглашению, аккаунт не нужен.
  </p>
</section>
