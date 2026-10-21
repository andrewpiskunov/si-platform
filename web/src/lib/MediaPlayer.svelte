<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, errorText, bytes } from './api.ts';
  import {
    actionFor,
    prepareAction,
    type MediaView,
    type MediaStatus,
    type SessionView,
    type V2Command
  } from './game-v2.ts';
  import { ServerClock, CurrentResource, playbackPlan } from './media-model.ts';
  let {
    media,
    view,
    seq,
    you,
    roomID,
    token,
    connected,
    paused,
    onsend
  }: {
    media: MediaView;
    view: SessionView;
    seq: number;
    you: string;
    roomID: string;
    token: string;
    connected: boolean;
    paused: boolean;
    onsend: (command: V2Command) => void;
  } = $props();
  let quality = $state<'standard' | 'economy'>('standard'),
    url = $state(''),
    loaded = $state(false),
    error = $state(''),
    autoplay = $state(false),
    hidden = $state(false),
    show = $state(false),
    retry = $state(0);
  let element: HTMLMediaElement | undefined = $state(),
    imageElement: HTMLImageElement | undefined = $state();
  const clock = new ServerClock(),
    resources = new CurrentResource();
  let identity = '',
    refresh: ReturnType<typeof setTimeout> | undefined,
    playing = false,
    alive = true,
    lastSent = '',
    pendingStage = '';
  const resourceKey = $derived(`${view.stage.instance_id}:${media.id}:${quality}`);
  const stageID = $derived(view.stage.id);
  $effect(() => {
    clock.observe(view.server_time_ms, performance.now());
  });
  $effect(() => {
    const key = resourceKey;
    const online = connected;
    void retry;
    // Snapshot and readiness changes must not replace a prepared native resource.
    return untrack(() => {
      if (key !== identity) {
        identity = key;
        element?.pause();
        element?.removeAttribute('src');
        element?.load();
        show = false;
        url = '';
        loaded = false;
        error = '';
        autoplay = false;
        lastSent = '';
      }
      if (!online) {
        resources.close();
        pendingStage = '';
        element?.pause();
        return;
      }
      pendingStage = stageID;
      const request = resources.begin(key);
      void loadTicket(request, quality);
      return () => {
        resources.close();
        pendingStage = '';
        clearTimeout(refresh);
      };
    });
  });
  $effect(() => {
    const stage = stageID;
    untrack(() => {
      if (pendingStage && pendingStage !== stage) {
        resources.close();
        pendingStage = '';
        retry++;
      }
    });
  });
  async function loadTicket(
    request: ReturnType<CurrentResource['begin']>,
    chosen: 'standard' | 'economy'
  ) {
    try {
      const ticket = await api<{ url: string; expires_at: number }>(
        `/rooms/${roomID}/media-ticket`,
        'POST',
        { media_id: media.id, quality: chosen },
        token,
        request.signal
      );
      if (!resources.current(request) || !alive) return;
      error = '';
      url = ticket.url;
      clearTimeout(refresh);
      refresh = setTimeout(
        () => retry++,
        Math.max(1000, ticket.expires_at * 1000 - clock.now(performance.now()) - 10000)
      );
    } catch (e) {
      if (resources.current(request) && alive) {
        error = errorText(e);
        loaded = false;
      }
    } finally {
      if (resources.current(request)) pendingStage = '';
    }
  }
  function report(status: MediaStatus) {
    if (!connected || !actionFor(view, 'media_ready')) return;
    const key = view.stage.id + ':' + status;
    if (lastSent === key || view.readiness?.find((p) => p.id === you)?.status === status) return;
    try {
      const command = prepareAction(view, seq, 'media_ready', { status });
      lastSent = key;
      onsend(command);
    } catch {
      /* A newer snapshot may already have closed readiness. */
    }
  }
  $effect(() => {
    void view.stage.id;
    void view.stage.kind;
    void view.allowed_actions;
    void connected;
    report(hidden ? 'hidden' : error || autoplay ? 'error' : loaded ? 'ready' : 'loading');
  });
  function ready() {
    loaded = true;
    error = '';
    sync();
  }
  function failed() {
    if (!url) return;
    loaded = false;
    error = 'Не удалось загрузить медиа. Повторите загрузку или выберите экономное качество.';
  }
  function sync(gesture = false) {
    const plan = playbackPlan(media, paused, clock.now(performance.now()), view.stage.kind);
    show = loaded && plan.show;
    if (!element) return;
    if (!connected || !loaded) {
      element.pause();
      return;
    }
    if (!plan.play) element.pause();
    if (element.readyState < 2) return;
    if (Math.abs(element.currentTime - plan.position) > (plan.play ? 0.35 : 0.01)) {
      try {
        element.currentTime = plan.position;
      } catch {}
    }
    if (!plan.play) return;
    if (element.paused && !playing && (!autoplay || gesture)) {
      playing = true;
      const target = element;
      void target
        .play()
        .then(() => {
          if (alive && target === element) autoplay = false;
        })
        .catch((e) => {
          if (alive && target === element && e?.name !== 'AbortError') autoplay = true;
        })
        .finally(() => (playing = false));
    }
  }
  onMount(() => {
    const visibility = () => {
      hidden = document.visibilityState === 'hidden';
    };
    visibility();
    document.addEventListener('visibilitychange', visibility);
    const interval = setInterval(() => sync(), 100);
    return () => {
      alive = false;
      clearInterval(interval);
      clearTimeout(refresh);
      resources.close();
      document.removeEventListener('visibilitychange', visibility);
      element?.pause();
      element?.removeAttribute('src');
      element?.load();
      imageElement?.removeAttribute('src');
    };
  });
</script>

<section class="media-player stack" aria-label="Медиафрагмент">
  <div class="row between">
    <span class="helper"
      >{media.kind === 'image' ? 'Изображение' : media.kind === 'audio' ? 'Аудио' : 'Видео'} · {bytes(
        media[quality].size
      )}</span
    ><label
      >Качество медиа<select bind:value={quality}
        ><option value="standard">Обычное</option><option value="economy">Экономное</option></select
      ></label
    >
  </div>
  <div class="media-frame" class:media-waiting={!show}>
    {#if url}{#if media.kind === 'image'}<img
          bind:this={imageElement}
          src={url}
          alt="Изображение текущего фрагмента"
          class:media-concealed={!show}
          onload={ready}
          onerror={failed}
          referrerpolicy="no-referrer"
        />{:else if media.kind === 'audio'}<audio
          bind:this={element}
          src={url}
          preload="auto"
          controls
          class:media-concealed={!show}
          oncanplay={ready}
          onloadeddata={ready}
          onerror={failed}
        ></audio>{:else}<video
          bind:this={element}
          src={url}
          preload="auto"
          playsinline
          controls
          class:media-concealed={!show}
          oncanplay={ready}
          onloadeddata={ready}
          onerror={failed}><track kind="captions" /></video
        >{/if}{/if}
    {#if !show}<p class="media-placeholder" role="status">
        {error
          ? 'Файл недоступен'
          : loaded
            ? 'Медиа готово. Ждём общего старта.'
            : 'Загружаем текущий фрагмент…'}
      </p>{/if}
  </div>
  {#if paused}<p class="helper">Пауза · позиция сохранена</p>{/if}
  {#if autoplay}<div class="notice">
      <p>Браузер ожидает вашего разрешения на звук и видео.</p>
      <button
        class="primary"
        onclick={() => {
          autoplay = false;
          sync(true);
        }}>Включить воспроизведение</button
      >
    </div>{/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <button
    class="small ghost"
    onclick={() => {
      url = '';
      loaded = false;
      error = '';
      autoplay = false;
      retry++;
    }}>Повторить загрузку медиа</button
  >
</section>
