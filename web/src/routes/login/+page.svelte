<script lang="ts">
  import Icon from '#lib/Icon.svelte';
  import { page } from '$app/state';
  import { onMount } from 'svelte';
  import { goto, replaceState } from '$app/navigation';
  import { api, APIError, errorText } from '#lib/api.ts';
  import { refreshUser, authenticationChanged } from '#lib/session.ts';
  type Mode = 'login' | 'register' | 'forgot' | 'reset' | 'verify';
  let mode = $state<Mode>('login'),
    identity = $state(''),
    password = $state(''),
    confirmPassword = $state(''),
    showPassword = $state(false),
    showConfirmation = $state(false),
    mismatch = $state(false),
    displayName = $state(''),
    linkToken = $state(''),
    busy = $state(false),
    error = $state(''),
    message = $state('');
  const newPassword = $derived(mode === 'register' || mode === 'reset');
  const titles: Record<Mode,string> = {login:'Вход',register:'Создать аккаунт',forgot:'Восстановить пароль',reset:'Новый пароль',verify:'Подтвердить почту'};
  onMount(() => {
    const requested = page.url.searchParams.get('mode');
    if (requested && requested in titles) mode = requested as Mode;
    linkToken = new URLSearchParams(window.location.hash.slice(1)).get('token') || '';
    // Keep bearer tokens out of subsequent URL copies and browser history.
    if (window.location.hash) replaceState(window.location.pathname+window.location.search,page.state);
    if (page.url.searchParams.get('reset') === 'done') message='Пароль изменён. Войдите с новым паролем. Предыдущие сессии завершены.';
  });
  function changeMode(next: Mode) {
    mode=next; password=''; confirmPassword=''; showPassword=false; showConfirmation=false;
    mismatch=false; error=''; message='';
  }
  async function enter() {
    password='';confirmPassword='';linkToken='';
    authenticationChanged();await refreshUser();
    const next = page.url.searchParams.get('next');
    await goto(next && (next === '/themes' || next === '/rooms?create=1' || /^\/packs\/[a-f0-9]{32}\?create=1$/.test(next)) ? next : '/studio');
  }
  async function submit(event: SubmitEvent) {
    event.preventDefault();if(busy)return;
    error='';message='';mismatch=newPassword && password!==confirmPassword;
    if(mismatch){document.getElementById('confirm-password')?.focus();return;}
    busy=true;
    try {
      if(mode==='register') {
        await api('/account/register','POST',{email:identity.trim(),password,display_name:displayName.trim()});
        password='';confirmPassword='';showPassword=false;showConfirmation=false;
        message='Если адрес ещё не зарегистрирован, мы отправили письмо. Откройте ссылку, чтобы подтвердить почту и создать аккаунт. Ссылка действует 24 часа. Если аккаунт уже есть, войдите или восстановите пароль.';
      } else if(mode==='forgot') {
        await api('/account/forgot-password','POST',{email:identity.trim()});
        message='Если аккаунт с такой почтой существует, мы отправили ссылку для восстановления. Она действует 30 минут. Проверьте также папку «Спам».';
      } else if(mode==='reset') {
        await api('/account/reset-password','POST',{token:linkToken,password});
        linkToken='';password='';confirmPassword='';
        await goto('/login?reset=done',{replaceState:true});
        authenticationChanged();
      } else if(mode==='verify') {
        await api('/account/verify-email','POST',{token:linkToken});await enter();
      } else {
        await api('/account/login','POST',{username:identity.trim(),password});await enter();
      }
    } catch(e) {
      if(e instanceof APIError && e.code==='unauthorized' && mode==='login') error='Неверная почта, логин или пароль.';
      else if(e instanceof APIError && e.code==='invalid_input' && (mode==='verify'||mode==='reset')) error='Ссылка недействительна или срок её действия истёк. Запросите новое письмо.';
      else error=errorText(e);
    } finally {busy=false;}
  }
</script>

<svelte:head><title>{titles[mode]} — Своя игра</title><meta name="referrer" content="no-referrer" /></svelte:head>
<section class="page narrow">
  <h1>{titles[mode]}</h1>
  <p class="muted">Аккаунт нужен для создания пакетов, комнат и своих тем оформления.</p>
  <div class="panel auth-card">
    <div class="auth-tabs">
      <button disabled={busy} class:primary={mode==='login'} onclick={()=>changeMode('login')}>Вход</button>
      <button disabled={busy} class:primary={mode==='register'} onclick={()=>changeMode('register')}>Регистрация</button>
    </div>
    <form class="stack" onsubmit={submit}>
      {#if mode==='login'||mode==='register'||mode==='forgot'}
        <div class="stack gap-sm">
          <label for="auth-identity">{mode==='login'?'Почта или логин':'Почта'}</label>
          <input id="auth-identity" type={mode==='login'?'text':'email'} bind:value={identity}
            autocomplete={mode==='login'?'username':'email'} required maxlength="254"
            autocapitalize="none" spellcheck="false" placeholder="you@example.com" />
          <span class="helper">{mode==='login'?'Для старых аккаунтов работает вход по логину.':'Адрес для подтверждения и восстановления доступа. Другие игроки его не увидят.'}</span>
        </div>
      {/if}
      {#if mode==='register'}
        <div class="stack gap-sm">
          <label for="display-name">Имя в игре</label>
          <input id="display-name" bind:value={displayName} autocomplete="nickname" required maxlength="64" placeholder="Как вас зовут?" />
          <span class="helper">На любом языке, до 64 символов. Это имя увидят другие игроки.</span>
        </div>
      {/if}
      {#if mode==='login'||newPassword}
        <div class="stack gap-sm">
          <label for="auth-password">{mode==='reset'?'Новый пароль':'Пароль'}</label>
          <div class="password-field">
            <input id="auth-password" type={showPassword?'text':'password'} bind:value={password}
              oninput={()=>{mismatch=false;}} autocomplete={newPassword?'new-password':'current-password'}
              required minlength={newPassword?12:1} maxlength="128" />
            <button type="button" aria-label={showPassword?'Скрыть пароль':'Показать пароль'} aria-pressed={showPassword}
              onclick={()=>{showPassword=!showPassword;}}><Icon name={showPassword?'eye-off':'eye'} /></button>
          </div>
          {#if newPassword}<span class="helper">От 12 символов</span>{/if}
        </div>
      {/if}
      {#if newPassword}
        <div class="stack gap-sm">
          <label for="confirm-password">Повторите пароль</label>
          <div class="password-field">
            <input id="confirm-password" type={showConfirmation?'text':'password'} bind:value={confirmPassword}
              oninput={()=>{mismatch=false;}} autocomplete="new-password" required minlength="12" maxlength="128"
              aria-invalid={mismatch} aria-describedby={mismatch?'password-mismatch':undefined} />
            <button type="button" aria-label={showConfirmation?'Скрыть повтор пароля':'Показать повтор пароля'} aria-pressed={showConfirmation}
              onclick={()=>{showConfirmation=!showConfirmation;}}><Icon name={showConfirmation?'eye-off':'eye'} /></button>
          </div>
          {#if mismatch}<p id="password-mismatch" role="alert">Пароли не совпадают. Проверьте повтор пароля.</p>{/if}
        </div>
      {/if}
      {#if mode==='verify'}<p>Подтвердите адрес почты, чтобы завершить создание аккаунта.</p>{/if}
      {#if (mode==='verify'||mode==='reset')&&!linkToken}<p role="alert">Откройте полную ссылку из письма или запросите новое письмо.</p>{/if}
      {#if error}<div class="notice error" role="alert">{error}</div>{/if}
      {#if message}<div class="notice" role="status">{message}</div>{/if}
      <button class="primary" disabled={busy||((mode==='verify'||mode==='reset')&&!linkToken)}>
        {busy?'Подождите…':mode==='register'?'Создать аккаунт':mode==='forgot'?'Отправить ссылку':mode==='reset'?'Сохранить пароль':mode==='verify'?'Подтвердить почту':'Войти'}
      </button>
      {#if mode==='login'||mode==='reset'}<button type="button" disabled={busy} onclick={()=>changeMode('forgot')}>Забыли пароль?</button>{/if}
    </form>
  </div>
  <p class="helper" style="margin-top:20px">Чтобы присоединиться к игре по приглашению, аккаунт не нужен.</p>
</section>
<style>
  .password-field{display:flex;gap:8px;align-items:stretch;}
  .password-field input{min-width:0;flex:1;}
  .password-field button{flex-shrink:0;width:48px;min-height:48px;padding:10px;display:grid;place-items:center;}
  #password-mismatch{margin:0;}
</style>
