<script lang="ts">
  import '../app.css';
  import { readRoomCredentials } from '#lib/room-credentials.ts';
  import { onMount, setContext, untrack } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { page } from '$app/state';
  import { refreshUser, user, sessionEpoch, watchAuthentication } from '#lib/session.ts';
  import { createRoomTheme, roomThemeContext, AppearanceConnection } from '#lib/room-theme.ts';
  import { roomContext, contextLink, themePreferences } from '#lib/appearance.ts';
  import ThemePreferences from '#lib/ThemePreferences.svelte';
  const roomTheme = createRoomTheme(),
    themeDetail = roomTheme.detail;
  const context = $derived(roomContext(page.url));
  const inGame = $derived(page.url.pathname.startsWith('/rooms/'));
  let mounted = $state(false),
    watcher: AppearanceConnection | null = null;
  beforeNavigate(({ to }) => {
    if (!to || to.url.pathname.startsWith('/rooms/') || roomContext(to.url) !== context) {
      watcher?.close();
      watcher = null;
    }
  });
  $effect(() => {
    if (!mounted) return;
    const room = context,
      game = inGame;
    untrack(() => {
      let token = readRoomCredentials(room)?.token || '',
        preferences = themePreferences(null, matchMedia('(max-width: 700px)').matches);
      try {
        preferences = themePreferences(
          sessionStorage.getItem(`appearance:${room}`),
          matchMedia('(max-width: 700px)').matches
        );
      } catch {}
      roomTheme.enter(room, token, preferences);
      if (room && token && !game) {
        watcher = new AppearanceConnection(
          room,
          token,
          () => void roomTheme.refresh(),
          () => roomTheme.reset()
        );
        watcher.connect();
      }
    });
    return () => {
      watcher?.close();
      watcher = null;
    };
  });
  setContext(roomThemeContext, roomTheme);
  let { children } = $props();
  onMount(() => {
    mounted = true;
    const stopAuthentication = watchAuthentication();
    void refreshUser();
    const visible = () => {
      if (document.visibilityState === 'visible') void roomTheme.refresh();
    };
    document.addEventListener('visibilitychange', visible);
    return () => {
      stopAuthentication();
      document.removeEventListener('visibilitychange', visible);
      watcher?.close();
      roomTheme.reset();
    };
  });
</script>

<svelte:head
  ><title>Своя игра — соберите свой вечер</title><meta
    name="description"
    content="Выбирайте пакеты вопросов, создавайте свои и играйте с друзьями в браузере."
  /></svelte:head
>
<div
  class="app-shell"
  data-theme={$roomTheme || ($themeDetail.manifest ? 'custom' : undefined)}
  style={$themeDetail.style}
>
  {#if $themeDetail.manifest?.background && $themeDetail.images[$themeDetail.manifest.background]}<img
      class="theme-background"
      src={$themeDetail.images[$themeDetail.manifest.background]}
      alt=""
      aria-hidden="true"
    />{/if}
  {#if $themeDetail.manifest?.ornament && $themeDetail.images[$themeDetail.manifest.ornament]}<img
      class="theme-ornament"
      src={$themeDetail.images[$themeDetail.manifest.ornament]}
      alt=""
      aria-hidden="true"
    />{:else if $roomTheme === 'pirate' || $roomTheme === 'anime'}<img
      class="theme-ornament builtin-art"
      src={`/themes/${$roomTheme}.svg`}
      alt=""
      aria-hidden="true"
    />{/if}
  <a class="skip" href="#main">К содержимому</a>
  <header class="topbar">
    <a class="brand" href={contextLink('/', context)} aria-label="Своя игра — главная"
      ><span class="brand-mark" aria-hidden="true"
        >{#if $themeDetail.manifest?.logo && $themeDetail.images[$themeDetail.manifest.logo]}<img
            class="theme-logo"
            src={$themeDetail.images[$themeDetail.manifest.logo]}
            alt=""
          />{:else}С{/if}</span
      ><span>Своя<span class="brand-light"> игра</span></span></a
    >
    <nav aria-label="Основная навигация">
      <a class:active={page.url.pathname === '/'} href={contextLink('/', context)}>Каталог</a><a
        class:active={page.url.pathname === '/rooms'}
        href={contextLink('/rooms', context)}>Комнаты</a
      ><a
        class:active={page.url.pathname.startsWith('/studio')}
        href={contextLink('/studio', context)}>Мастерская</a
      ><a class:active={page.url.pathname === '/themes'} href={contextLink('/themes', context)}
        >Оформление</a
      >{#if $user && ['moderator', 'admin'].includes($user.role)}<a
          href={contextLink('/moderation', context)}>Модерация</a
        >{/if}
    </nav>
    <a class="account-link" href={contextLink($user ? '/account' : '/login', context)}
      ><span class="avatar">{$user?.display_name?.slice(0, 1) || '↗'}</span><span
        >{$user?.display_name || 'Войти'}</span
      ></a
    >
  </header>
  <main id="main">
    {#if context}<div class="theme-context-bar">
        <a class="text-link" href={`/rooms/${context}`}>← В комнату</a><ThemePreferences />
      </div>{/if}{#if $themeDetail.notice}<p class="notice theme-fallback" role="status">
        {$themeDetail.notice}
      </p>{/if}{#key $sessionEpoch}{@render children()}{/key}
  </main>
  <footer class="site-footer">
    <a href="/privacy">Хранение и удаление данных</a>
    <span>Своя игра <span class="muted">· Вопросы, которые нас собирают</span></span><a
      href={contextLink('/studio', context)}>Создать свой пакет ↗</a
    >
  </footer>
</div>
