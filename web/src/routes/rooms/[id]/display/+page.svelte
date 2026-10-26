<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { RoomConnection, type RoomState } from '#lib/room-client.ts';
  import { readRoomCredentials } from '#lib/room-credentials.ts';
  import { api, errorText } from '#lib/api.ts';
  import type { Board } from '#lib/types.ts';
  import GameSounds from '#lib/GameSounds.svelte';
  import PublicGameStage from '#lib/PublicGameStage.svelte';
  import PublicGameScores from '#lib/PublicGameScores.svelte';
  import GameTimer from '#lib/GameTimer.svelte';
  let roomState = $state<RoomState | null>(null),
    board = $state<Board | null>(null);
  let error = $state(''),
    connected = $state(false),
    token = $state('');
  const room = $derived(page.params.id || '');
  const view = $derived(roomState?.gameplay);
  onMount(() => {
    const credentials = readRoomCredentials(room);
    if (!credentials) {
      error = 'Откройте второй экран кнопкой в комнате ведущего.';
      return;
    }
    token = credentials.token;
    let active = true;
    let boardKey = '';
    const connection = new RoomConnection(
      room,
      token,
      (protocol, status, code) => {
        connected = status === 'connected';
        if (code) error = errorText(code);
        if (protocol.state) {
          roomState = protocol.state;
          const key = `${roomState.gameplay?.round?.id}:${roomState.gameplay?.stage.id}`;
          if (key !== boardKey) {
            boardKey = key;
            void api<Board>(`/rooms/${room}/board`, 'GET', undefined, token)
              .then((value) => {
                if (active && key === boardKey) board = value;
              })
              .catch(() => {});
          }
        }
      },
      true
    );
    void connection.connect();
    return () => {
      active = false;
      connection.close();
    };
  });
</script>

<svelte:head><title>Второй экран — Своя игра</title></svelte:head>
<main class="display-screen">
  <div class="display-header">
    <h1>{board?.title || 'Своя игра'}</h1>
    {#if view && roomState}<GameSounds
        {view}
        seq={roomState.seq}
        phase={roomState.phase}
        ready={connected}
      />{/if}<button
      onclick={() =>
        document.documentElement.requestFullscreen().catch(() => {
          error = 'Включите полноэкранный режим через меню браузера.';
        })}>На весь экран</button
    >
  </div>
  {#if error}<p role="alert">{error}</p>{/if}
  {#if !connected}<p role="status">{error ? '' : 'Подключаем второй экран…'}</p>{/if}
  {#if view && roomState}
    {#if roomState.phase !== 'finished'}<div class="display-header">
        <h2>
          {roomState.phase === 'lobby'
            ? 'Собираемся играть'
            : roomState.phase === 'paused'
              ? 'Пауза'
              : view.question?.theme || view.round?.name || ''}
        </h2>
        <GameTimer
          stage={view.stage}
          serverTime={view.server_time_ms}
          paused={roomState.phase === 'paused'}
        />
      </div>{/if}
    <div class="display-scene stack">
      <PublicGameStage room={roomState} {view} {board} roomToken={token} ready={connected} />
    </div>
    <PublicGameScores room={roomState} {view} />
  {/if}
</main>

<style>
  :global(body:has(.display-screen) .topbar),
  :global(body:has(.display-screen) .site-footer) {
    display: none;
  }
  .display-screen {
    padding: 32px;
    min-height: 100dvh;
  }
  .display-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 24px;
    margin-bottom: 24px;
  }
  h1,
  h2 {
    margin: 0;
  }
  .display-scene {
    min-height: 50dvh;
    display: flex;
    flex-direction: column;
  }
  .display-scene :global(.question-stage) {
    flex: 1;
    min-height: 50dvh;
  }
  .display-screen :global(.score-list) {
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  }
  @media (max-width: 700px) {
    .display-screen {
      padding: 16px;
    }
    .display-header {
      flex-wrap: wrap;
      gap: 12px;
    }
  }
</style>
