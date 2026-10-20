<script lang="ts">
  import '../app.css';
  import { onMount, setContext } from 'svelte';
  import { page } from '$app/state';
  import { refreshUser, user } from '#lib/session.ts';
  import { createRoomTheme, roomThemeContext } from '#lib/room-theme.ts';
  const roomTheme = createRoomTheme();
  setContext(roomThemeContext, roomTheme);
  let { children } = $props();
  onMount(() => {
    void refreshUser();
  });
</script>

<svelte:head
  ><title>Своя игра — соберите свой вечер</title><meta
    name="description"
    content="Выбирайте пакеты вопросов, создавайте свои и играйте с друзьями в браузере."
  /></svelte:head
>
<div class="app-shell" data-theme={$roomTheme || undefined}>
  <a class="skip" href="#main">К содержимому</a>
  <header class="topbar">
    <a class="brand" href="/" aria-label="Своя игра — главная"
      ><span class="brand-mark" aria-hidden="true">С</span><span
        >Своя<span class="brand-light"> игра</span></span
      ></a
    >
    <nav aria-label="Основная навигация">
      <a class:active={page.url.pathname === '/'} href="/">Каталог</a><a
        class:active={page.url.pathname.startsWith('/studio')}
        href="/studio">Мастерская</a
      >{#if $user && ['moderator', 'admin'].includes($user.role)}<a href="/moderation">Модерация</a
        >{/if}
    </nav>
    <a class="account-link" href={$user ? '/account' : '/login'}
      ><span class="avatar">{$user?.display_name?.slice(0, 1) || '↗'}</span><span
        >{$user?.display_name || 'Войти'}</span
      ></a
    >
  </header>
  <main id="main">{@render children()}</main>
  <footer class="site-footer">
    <span>Своя игра <span class="muted">· Вопросы, которые нас собирают</span></span><a
      href="/studio">Создать свой пакет ↗</a
    >
  </footer>
</div>
