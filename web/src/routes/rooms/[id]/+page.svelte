<script lang="ts">
  import {
    readRoomCredentials,
    saveRoomCredentials,
    clearRoomCredentials
  } from '#lib/room-credentials.ts';
  import { onMount, getContext } from 'svelte';
  import { page } from '$app/state';
  import { api, APIError, errorText } from '#lib/api.ts';
  import { RoomConnection, type RoomState } from '#lib/room-client.ts';
  import { BoardLoader } from '#lib/board-loader.ts';
  import type { Board, Credentials } from '#lib/types.ts';
  import GameV2 from '#lib/GameV2.svelte';
  import GameResults from '#lib/GameResults.svelte';
  import { actionFor, prepareAction, type V2Command } from '#lib/game-v2.ts';
  import { roomThemeContext, type RoomTheme } from '#lib/room-theme.ts';
  import ThemeApply from '#lib/ThemeApply.svelte';
  import { themePreferences } from '#lib/appearance.ts';
  import { normalizedSettings } from '#lib/room-preferences.ts';
  import VoiceLink from '#lib/VoiceLink.svelte';
  const roomTheme = getContext<RoomTheme>(roomThemeContext);
  let credentials = $state<Credentials | null>(null),
    roomState = $state<RoomState | null>(null),
    board = $state<Board | null>(null),
    boardError = $state(''),
    boardLoading = $state(false),
    name = $state(''),
    role = $state('player'),
    invite = $state(''),
    joining = $state(false),
    ready = $state(false),
    error = $state(''),
    connectionStatus = $state('connecting'),
    pending = $state(0),
    copied = $state(false),
    invitationClosed = $state(false),
    roundIndex = $state(0),
    selection = $state<{
      question_id?: string;
      round_id?: string;
      replay?: boolean;
      command_id: string;
      expected_seq: number;
    } | null>(null),
    selecting = $state(false),
    confirmFinish = $state(false),
    mounted = $state(false),
    resultsOpen = $state(false),
    rotating = $state(false),
    revoked = $state(false);
  let connection: RoomConnection | null = null;
  let roomID = $state('');
  const boardLoader = new BoardLoader<Board>();
  let alive = true;
  const me = $derived(
    roomState?.participants.find((participant) => participant.id === roomState?.you)
  );
  const players = $derived(
    (roomState?.participants || []).filter(
      (participant) => participant.role === 'player' && !participant.removed
    )
  );
  const isHost = $derived(me?.role === 'host');
  const canBuzz = $derived(
    ready &&
      pending === 0 &&
      (roomState?.gameplay
        ? !!actionFor(roomState.gameplay, 'buzz')
        : roomState?.phase === 'open' && me?.role === 'player')
  );
  const between = $derived(roomState?.phase === 'lobby' || roomState?.phase === 'resolved');
  const phaseNames: Record<string, string> = {
    lobby: 'Собираемся',
    open: 'Вопрос открыт',
    running: 'Идёт игра',
    answering: 'Есть ответ!',
    resolved: 'Ответ разобран',
    paused: 'Пауза',
    finished: 'Игра завершена'
  };
  onMount(() => {
    mounted = true;
    return () => {
      alive = false;
      connection?.close();
      boardLoader.reset();
    };
  });
  $effect(() => {
    const id = page.params.id || '';
    if (mounted && id !== roomID) initialize(id);
  });
  function initialize(id: string) {
    connection?.close();
    boardLoader.reset();
    roomID = id;
    revoked = false;
    board = null;
    boardError = '';
    boardLoading = false;
    roomState = null;
    ready = false;
    credentials = null;
    selection = null;
    invitationClosed = false;
    role = page.url.searchParams.get('role') === 'spectator' ? 'spectator' : 'player';
    invite = new URLSearchParams(location.hash.slice(1)).get('invite') || '';
    credentials = readRoomCredentials(roomID);
    if (credentials) connect();
  }
  function connect() {
    if (!credentials) return;
    connection?.close();
    let preferences = null;
    try {
      preferences = sessionStorage.getItem(`appearance:${roomID}`);
    } catch {}
    roomTheme.enter(
      roomID,
      credentials.token,
      themePreferences(preferences, matchMedia('(max-width: 700px)').matches)
    );
    const connectedRoom = roomID;
    connection = new RoomConnection(roomID, credentials.token, (protocol, status, code) => {
      if (!alive || connectedRoom !== roomID) return;
      if (status === 'revoked') {
        releaseRevoked();
        return;
      }
      const previousVisibility = roomState
        ? normalizedSettings(roomState.settings).visibility
        : null;
      roomState = protocol.state;
      if (
        previousVisibility === 'public' &&
        roomState &&
        normalizedSettings(roomState.settings).visibility === 'private'
      ) {
        invite = '';
        copied = false;
        invitationClosed = true;
        history.replaceState(history.state, '', `${location.pathname}${location.search}`);
      }
      ready = protocol.ready;
      connectionStatus = status;
      pending = protocol.pending.size;
      if (code) error = errorText(code);
      roomTheme.observe(roomState?.appearance, protocol.appearanceEpoch, protocol.ready);
      if (roomState && protocol.ready) void loadBoard();
    });
    connection.connect();
  }
  async function loadBoard(force = false) {
    if (!credentials || !roomState) return;
    const loadingRoom = roomID;
    const key = `${loadingRoom}:${roomState.gameplay?.round?.id || ''}:${roomState.gameplay?.stage.instance_id || roomState.question?.id || ''}:${roomState.phase}`;
    const token = credentials.token;
    try {
      const pending = boardLoader.load(
        key,
        (signal) => api<Board>(`/rooms/${loadingRoom}/board`, 'GET', undefined, token, signal),
        force
      );
      boardLoading = boardLoader.pending;
      const result = await pending;
      if (alive && loadingRoom === roomID && result) {
        board = result;
        boardError = '';
        if (roundIndex >= result.rounds.length) roundIndex = 0;
      }
    } catch (e) {
      if (alive && loadingRoom === roomID) {
        if (e instanceof APIError && e.status === 401) connection?.revoke();
        else boardError = errorText(e);
      }
    } finally {
      if (alive && loadingRoom === roomID) boardLoading = boardLoader.pending;
    }
  }
  async function join(event: SubmitEvent) {
    event.preventDefault();
    joining = true;
    error = '';
    try {
      credentials = await api<Credentials>(`/rooms/${roomID}/participants`, 'POST', {
        name: name.trim(),
        role,
        invite_token: invite
      });
      saveRoomCredentials(credentials);
      connect();
    } catch (e) {
      error =
        e instanceof APIError && e.status === 401
          ? 'Приглашение недействительно или устарело. Попросите ведущего прислать новую полную ссылку.'
          : errorText(e);
    } finally {
      joining = false;
    }
  }
  function send(type: string, fields: { question_id?: string; correct?: boolean } = {}) {
    error = '';
    try {
      connection?.send(type, fields);
    } catch (e) {
      error = errorText(e);
    }
  }
  function sendV2(command: V2Command) {
    error = '';
    try {
      connection?.sendV2(command);
    } catch (e) {
      error = errorText(e);
    }
  }
  function buzz() {
    if (canBuzz && roomState?.gameplay) {
      try {
        sendV2(prepareAction(roomState.gameplay, roomState.seq, 'buzz'));
      } catch (e) {
        error = errorText(e);
      }
      return;
    }
    if (canBuzz && roomState?.question) send('buzz', { question_id: roomState.question.id });
  }
  function keydown(event: KeyboardEvent) {
    if (
      event.code === 'Space' &&
      !event.repeat &&
      !(
        event.target instanceof HTMLElement &&
        event.target.closest('input,textarea,select,button,a')
      ) &&
      canBuzz
    ) {
      event.preventDefault();
      buzz();
    }
  }
  async function roomAction(kind: 'select' | 'round', id: string, retry = false, replay = false) {
    if (!credentials || !roomState || !ready || selecting) return;
    if (
      !retry &&
      roomState.gameplay &&
      !actionFor(roomState.gameplay, kind === 'select' ? 'begin_question' : 'begin_round')
    )
      return;
    error = '';
    selecting = true;
    if (!retry)
      selection = {
        ...(kind === 'select'
          ? { question_id: id, ...(replay ? { replay: true } : {}) }
          : { round_id: id }),
        command_id: crypto.randomUUID(),
        expected_seq: roomState.seq
      };
    try {
      await api(`/rooms/${roomID}/${kind}`, 'POST', selection, credentials.token);
      selection = null;
      await loadBoard(true);
    } catch (e) {
      if (e instanceof APIError && e.status === 401) connection?.revoke();
      else error = errorText(e);
      if (e instanceof APIError && e.status < 500) selection = null;
    } finally {
      selecting = false;
    }
  }
  function choose(questionID: string, retry = false) {
    return roomAction('select', questionID, retry);
  }
  function retrySelection() {
    if (selection)
      return roomAction(
        selection.round_id ? 'round' : 'select',
        selection.round_id || selection.question_id || '',
        true
      );
  }
  async function rotateInvite() {
    if (!credentials || rotating) return;
    rotating = true;
    error = '';
    try {
      const result = await api<{ invite_token: string }>(
        `/rooms/${roomID}/invite`,
        'POST',
        {},
        credentials.token
      );
      invite = result.invite_token;
      invitationClosed = false;
      history.replaceState(
        history.state,
        '',
        `${location.pathname}#invite=${encodeURIComponent(invite)}`
      );
    } catch (e) {
      error = errorText(e);
    } finally {
      rotating = false;
    }
  }
  async function share() {
    try {
      await navigator.clipboard.writeText(
        `${location.origin}/rooms/${roomID}#invite=${encodeURIComponent(invite)}`
      );
      copied = true;
      setTimeout(() => (copied = false), 3000);
    } catch {
      error = 'Не удалось скопировать ссылку. Скопируйте её из поля приглашения.';
    }
  }
  function releaseRevoked() {
    connection?.close();
    clearRoomCredentials(roomID);
    boardLoader.reset();
    roomTheme.reset();
    credentials = null;
    roomState = null;
    board = null;
    boardError = '';
    boardLoading = false;
    ready = false;
    pending = 0;
    resultsOpen = false;
    selection = null;
    error = '';
    revoked = true;
  }
  function reset() {
    connection?.close();
    boardLoader.reset();
    boardError = '';
    boardLoading = false;
    clearRoomCredentials(roomID);
    revoked = false;
    board = null;
    credentials = null;
    roomTheme.enter(roomID, '', themePreferences(null));
    roomState = null;
    ready = false;
    resultsOpen = false;
    selection = null;
    error = '';
  }
</script>

<svelte:window onkeydown={keydown} />
<svelte:head
  ><title>{board?.title || 'Игровая комната'} — Своя игра</title><meta
    name="referrer"
    content="no-referrer"
  /></svelte:head
>
<section class="page">
  {#if mounted && credentials && connectionStatus === 'failed'}
    <div class="notice error" role="alert">
      <p>Подключение не удалось. Повторите явно.</p>
      <button onclick={() => connection?.connect()}>Повторить подключение</button>
    </div>
  {/if}
  {#if boardError}<div class="notice error board-error" role="alert">
      <p>{boardError}</p>
      <button class="small" disabled={boardLoading || !ready} onclick={() => void loadBoard(true)}
        >Повторить загрузку табло</button
      >
    </div>{/if}
  {#if !mounted}<p class="loading" role="status">Открываем комнату…</p>{:else if revoked}<div
      class="narrow panel stack"
      role="alert"
      aria-label="Доступ к комнате прекращён"
    >
      <h1>Доступ к комнате прекращён</h1>
      <p>
        Ваше место удалено или доступ отозван. Автоматического повторного входа не будет. После
        удаления можно войти заново с нулевым счётом, если вход разрешён; запрет сессии проверит
        сервер.
      </p>
      <button onclick={reset}>Войти заново по приглашению</button>
      <a class="button" href="/rooms">Выйти к списку комнат</a>
    </div>{:else if !credentials}<div class="narrow" style="margin:auto">
      <p class="eyebrow">{invite ? 'Вас пригласили играть' : 'Вход в комнату'}</p>
      <h1>Занимайте место.</h1>
      <p class="muted">
        Представьтесь и выберите роль. Вход произойдёт только после нажатия кнопки.
      </p>
      <form class="panel stack" onsubmit={join}>
        <label
          >Ваше имя<input
            bind:value={name}
            required
            maxlength="32"
            autocomplete="nickname"
            placeholder="Как вас представить?"
          /></label
        ><label
          >Роль<select bind:value={role}
            ><option value="player">Игрок — отвечаю и набираю очки</option><option value="spectator"
              >Зритель — смотрю игру</option
            ></select
          ></label
        >{#if !invite}<div class="notice">
            В публичную комнату можно войти без приглашения. Для приватной комнаты попросите
            ведущего прислать полную ссылку. Доступ и места проверяются при входе.
          </div>{/if}{#if error}<div class="notice error" role="alert">{error}</div>{/if}<button
          class="primary"
          disabled={joining}>{joining ? 'Подключаемся…' : 'Войти в комнату →'}</button
        >
      </form>
    </div>{:else if roomState?.gameplay}<div class="room">
      <div class="row between section-head">
        <div>
          <p class="eyebrow">{board?.pack_name || 'Своя игра'}</p>
          <h1 style="font-size:32px;margin-bottom:10px">{board?.title || 'Игровой вечер'}</h1>
          <span class="helper" role="status" aria-live="polite"
            >{ready
              ? '● В сети'
              : connectionStatus === 'reconnecting'
                ? 'Восстанавливаем соединение…'
                : 'Подключаемся…'} · {phaseNames[roomState.phase] || 'Игра'}</span
          >
        </div>
        {#if isHost}<div class="row">
            {#if invite}<button class="small" onclick={share}
                >{copied ? 'Ссылка скопирована' : 'Пригласить друзей ↗'}</button
              >{/if}<button class="small" disabled={rotating || !ready} onclick={rotateInvite}
              >{rotating
                ? 'Обновляем…'
                : invite
                  ? 'Обновить приглашение'
                  : 'Создать приглашение'}</button
            >
          </div>{/if}
      </div>
      {#if isHost && invitationClosed}<p class="notice" role="status">
          Комната теперь по приглашению. Прежние ссылки отключены; нажмите «Создать приглашение»,
          чтобы выдать новую.
        </p>{/if}
      {#if isHost && normalizedSettings(roomState.settings).visibility === 'public'}<p
          class="helper"
        >
          Обновление приглашения не закрывает свободный вход в публичную комнату.
        </p>{/if}
      {#if isHost && invite}<details class="invite-details">
          <summary>Ссылка приглашения</summary><input
            readonly
            value={`${location.origin}/rooms/${roomID}#invite=${encodeURIComponent(invite)}`}
            aria-label="Ссылка приглашения"
            onclick={(event) => event.currentTarget.select()}
          />
          <p class="helper">
            Обновление приглашения отключает прежнюю ссылку. Участники комнаты сохраняют свои места.
          </p>
        </details>{/if}
      {#if error}<div class="notice error" role="alert">
          {error}{#if selection}<button
              class="small"
              disabled={selecting || !ready}
              onclick={retrySelection}>Повторить действие</button
            >{/if}
        </div>{/if}
      {#if isHost}<ThemeApply
          room={roomID}
          token={credentials.token}
          seq={roomState.seq}
          phase={roomState.phase}
          appearance={roomState.appearance}
          {ready}
          onapplied={() => void roomTheme.refresh()}
        />{/if}
      <GameV2
        room={roomState}
        roomToken={credentials.token}
        view={roomState.gameplay}
        {board}
        {ready}
        pending={pending + (selection || selecting ? 1 : 0)}
        onsend={sendV2}
        onunauthorized={() => connection?.revoke()}
        onround={(id) => roomAction('round', id)}
        onselect={(id, replay) => roomAction('select', id, false, replay)}
        onresults={() => (resultsOpen = true)}
      />
      {#if resultsOpen}<GameResults
          {roomID}
          token={credentials.token}
          onclose={() => (resultsOpen = false)}
          onunauthorized={() => connection?.revoke()}
        />{/if}
    </div>{:else}<div class="room">
      {#if roomState}<VoiceLink value={normalizedSettings(roomState.settings).voice_url} />{/if}
      <div class="row between">
        <div>
          <p class="eyebrow">{board?.pack_name || 'Своя игра'}</p>
          <h1 style="font-size:32px;margin-bottom:10px">{board?.title || 'Игровой вечер'}</h1>
          <span class="helper" role="status" aria-live="polite"
            >{ready
              ? '● В сети'
              : connectionStatus === 'reconnecting'
                ? 'Восстанавливаем соединение…'
                : 'Подключаемся…'}{roomState ? ` · ${phaseNames[roomState.phase]}` : ''}</span
          >
        </div>
        <div class="row">
          {#if isHost && invite}<button class="small" onclick={share}
              >{copied ? 'Ссылка скопирована' : 'Пригласить друзей ↗'}</button
            >{/if}{#if isHost && roomState?.phase === 'paused'}<button
              class="primary small"
              disabled={!ready || pending > 0}
              onclick={() => send('resume')}>Продолжить ▶</button
            >{:else if isHost && ['open', 'answering'].includes(roomState?.phase || '')}<button
              class="small"
              disabled={!ready || pending > 0}
              onclick={() => send('pause')}>Пауза Ⅱ</button
            >{/if}
        </div>
      </div>
      {#if error}<div class="notice error" role="alert" style="margin-top:20px">
          {error}{#if selection}<button
              class="small"
              disabled={selecting || !ready}
              onclick={retrySelection}>Повторить выбор</button
            >{/if}
        </div>{/if}{#if !roomState}<div class="empty" style="margin-top:30px">
          <h2>Ждём соединение</h2>
          <p>
            {connectionStatus === 'failed'
              ? 'Подключение не удалось. Повторите явно.'
              : 'Ваше место восстановится автоматически.'}
          </p>

          <button class="small" onclick={reset}>Войти по приглашению заново</button>
        </div>{:else}<div class="room-grid">
          <div class="stack">
            {#if roomState.phase === 'finished'}<div class="question-stage">
                <p class="eyebrow">Спасибо за игру</p>
                <h2>
                  {[...players].sort((a, b) => b.score - a.score)[0]?.name || 'Вы'} — отличный вечер!
                </h2>
                <p class="muted">Итоговые очки — в таблице игроков.</p>
                <a class="button primary" href="/">Выбрать следующий пакет</a>
              </div>{:else if between}<div>
                <div class="row between" style="margin-bottom:20px">
                  <h2 style="margin:0">
                    {roomState.phase === 'lobby' ? 'Выбираем первый вопрос' : 'Продолжаем игру'}
                  </h2>
                  {#if board?.rounds.length}<select
                      style="width:auto;max-width:240px"
                      bind:value={roundIndex}
                      aria-label="Раунд"
                      >{#each board.rounds as round, index}<option value={index}
                          >{round.name}</option
                        >{/each}</select
                    >{/if}
                </div>
                {#if roomState.phase === 'lobby' && players.length < 2}<p class="notice">
                    Для начала игры нужны хотя бы два игрока. Пригласите друзей по ссылке.
                  </p>{/if}{#if roomState.phase === 'resolved' && roomState.question?.answer}<div
                    class="notice success"
                    style="margin-bottom:20px"
                  >
                    <strong>Ответ:</strong>
                    {roomState.question.answer}
                  </div>{/if}{#if board?.rounds[roundIndex]}<div class="board-scroll">
                    <div class="board">
                      {#each board.rounds[roundIndex].themes as theme}<div
                          class="board-row"
                          style={`--columns:${Math.max(1, theme.questions.length)}`}
                        >
                          <div class="board-theme">{theme.name}</div>
                          {#each theme.questions as question}<button
                              class="board-cell"
                              disabled={!isHost ||
                                !ready ||
                                question.played ||
                                selecting ||
                                players.length < 2}
                              onclick={() => choose(question.id)}
                              aria-label={`${theme.name}, ${question.value} очков${question.played ? ', сыгран' : ''}`}
                              >{question.played ? '—' : question.value}</button
                            >{/each}
                        </div>{/each}
                    </div>
                  </div>{:else}<p class="loading">Загружаем табло…</p>{/if}{#if !isHost}<p
                    class="helper"
                    style="margin-top:16px"
                  >
                    Ведущий выбирает следующий вопрос.
                  </p>{/if}
              </div>{:else}<div>
                <div class="question-stage">
                  {#if roomState.phase === 'paused'}<p class="eyebrow">Игра на паузе</p>{/if}
                  <div class="question-price">{roomState.question?.value} очков</div>
                  <h2>{roomState.question?.text}</h2>
                  {#if roomState.phase === 'answering'}<p class="pill">
                      Отвечает {roomState.participants.find(
                        (participant) => participant.id === roomState?.winner
                      )?.name}
                    </p>{/if}{#if isHost && roomState.question?.answer}<div
                      class="notice"
                      style="width:100%;margin-top:20px;text-align:left"
                    >
                      <span class="helper">Только для ведущего</span>
                      <p style="margin:8px 0 0">{roomState.question.answer}</p>
                    </div>{/if}{#if roomState.phase === 'paused'}<p
                      class="muted"
                      style="margin-top:24px"
                    >
                      Ведущий продолжит, когда все будут готовы.
                    </p>{/if}
                </div>
                {#if me?.role === 'player'}<button class="buzz" disabled={!canBuzz} onclick={buzz}
                    >{roomState.winner === roomState.you && roomState.phase === 'answering'
                      ? 'Ваш ответ!'
                      : 'Знаю ответ!'}</button
                  >
                  <p class="helper" style="text-align:center;margin-top:14px">
                    Кнопка или пробел на клавиатуре
                  </p>{/if}{#if isHost && roomState.phase === 'answering'}<div
                    class="row"
                    style="margin-top:20px"
                  >
                    <button
                      class="primary"
                      disabled={!ready || pending > 0}
                      onclick={() =>
                        send('judge', { question_id: roomState!.question!.id, correct: true })}
                      >✓ Верно · +{roomState.question?.value}</button
                    ><button
                      class="danger"
                      disabled={!ready || pending > 0}
                      onclick={() =>
                        send('judge', { question_id: roomState!.question!.id, correct: false })}
                      >× Неверно · −{roomState.question?.value}</button
                    >
                  </div>{/if}
              </div>{/if}
          </div>
          <aside>
            <div class="row between" style="margin-bottom:16px">
              <h3 style="margin:0">Игроки</h3>
              <span class="helper">{players.length} / 12</span>
            </div>
            <div class="score-list">
              {#each roomState.phase === 'finished' ? [...players].sort((a, b) => b.score - a.score) : players as participant}<div
                  class="score-item"
                  class:winner={participant.id === roomState.winner}
                >
                  <span>{participant.name}{participant.id === roomState?.you ? ' (вы)' : ''}</span
                  ><strong>{participant.score}</strong>
                </div>{/each}
            </div>
            <p class="helper" style="margin-top:20px">
              Ведущий: {roomState.participants.find((participant) => participant.role === 'host')
                ?.name}<br />Зрителей: {roomState.participants.filter(
                (participant) => participant.role === 'spectator'
              ).length}
            </p>
            {#if isHost && invite}<details style="margin-top:20px">
                <summary>Ссылка приглашения</summary><input
                  readonly
                  value={`${location.origin}/rooms/${roomID}#invite=${encodeURIComponent(invite)}`}
                  aria-label="Ссылка приглашения"
                  onclick={(event) => event.currentTarget.select()}
                />
              </details>{/if}{#if isHost && roomState.phase !== 'finished'}<div
                style="margin-top:30px"
              >
                {#if confirmFinish}<p class="helper">Завершить игру с текущим счётом?</p>
                  <div class="row">
                    <button
                      class="small danger"
                      disabled={!ready || pending > 0}
                      onclick={() => {
                        send('finish');
                        confirmFinish = false;
                      }}>Завершить</button
                    ><button class="small" onclick={() => (confirmFinish = false)}>Отмена</button>
                  </div>{:else}<button class="small ghost" onclick={() => (confirmFinish = true)}
                    >Завершить игру</button
                  >{/if}
              </div>{/if}
          </aside>
        </div>{/if}
    </div>{/if}
</section>
