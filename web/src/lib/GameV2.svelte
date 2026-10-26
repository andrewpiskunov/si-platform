<script lang="ts">
  import { onMount } from 'svelte';
  import PublicGameStage from './PublicGameStage.svelte';
  import PublicGameScores from './PublicGameScores.svelte';
  import QuestionMechanic from './QuestionMechanic.svelte';
  import GameBuzzer from './GameBuzzer.svelte';
  import Lobby from './Lobby.svelte';
  import {
    actionFor,
    isBoardStage,
    isPresentationStage,
    captureScope,
    prepareAction,
    stageNames,
    questionTypes,
    type SessionView,
    type V2Command
  } from './game-v2.ts';
  import { RoomConnection, type RoomState } from './room-client.ts';
  import type { Board } from './types.ts';
  import RoomParticipants from './RoomParticipants.svelte';
  import GameActionForm from './GameActionForm.svelte';
  import RoomPreferences from './RoomPreferences.svelte';
  import GameSounds from './GameSounds.svelte';
  import MediaPlayer from './MediaPlayer.svelte';
  import GameTimer from './GameTimer.svelte';
  import { errorText } from './api.ts';
  let {
    room,
    roomToken,
    view,
    board,
    ready,
    pending,
    onsend,
    onround,
    onselect,
    onresults,
    onunauthorized
  }: {
    room: RoomState;
    roomToken: string;
    view: SessionView;
    board: Board | null;
    ready: boolean;
    pending: number;
    onsend: (command: V2Command) => void;
    onround: (id: string) => void;
    onselect: (id: string, replay?: boolean) => void;
    onresults: () => void;
    onunauthorized: (room: string, token: string) => void;
  } = $props();
  let toolsDialog: HTMLDialogElement;
  let error = $state(''),
    selectedRound = $state(''),
    finishConfirm = $state(false),
    replay = $state(false);
  const round = $derived(
    board?.rounds.find((item) => item.id === (selectedRound || view.round?.id)) || board?.rounds[0]
  );
  const players = $derived(room.participants.filter((p) => p.role === 'player' && !p.removed));
  const isHost = $derived(room.participants.some((p) => p.id === room.you && p.role === 'host'));
  const roomID = $derived(room.room_id);
  let publicRoom = $state<RoomState | null>(null);
  let publicConnected = $state(false),
    publicError = $state('');
  let publicConnection: RoomConnection | undefined;
  const publicView = $derived(isHost ? publicRoom?.gameplay : view);
  onMount(() => {
    // Role changes must replace the projection as well as the private console.
    const stop = $effect.root(() => {
      $effect(() => {
        if (!isHost) {
          publicRoom = null;
          return;
        }
        const connection = new RoomConnection(
          roomID,
          roomToken,
          (protocol, status, code) => {
            publicConnected = status === 'connected';
            if (code) publicError = errorText(code);
            else if (status === 'connected') publicError = '';
            if (protocol.state) publicRoom = protocol.state;
          },
          true
        );
        publicConnection = connection;
        void connection.connect();
        return () => {
          connection.close();
          publicRoom = null;
          publicConnected = false;
        };
      });
    });
    return () => {
      stop();
    };
  });
  const blocked = $derived(!ready || pending > 0);
  const visibleSubmissions = $derived(
    (view.submissions || []).filter(
      (item) =>
        !item.revealed &&
        (item.answer !== undefined || item.wager !== undefined || item.correct !== undefined)
    )
  );
  const formTypes = new Set([
    'skip_question',
    'answer',
    'wager',
    'bid',
    'select_price',
    'select_recipient',
    'eliminate_theme',
    'judge',
    'reveal',
    'correct_judgement',
    'adjust_score',
    'appeal',
    'resolve_appeal',
    'transfer_host',
    'extend',
    'restart_stage'
  ]);
  const administrative = new Set([
    'correct_judgement',
    'adjust_score',
    'appeal',
    'resolve_appeal',
    'transfer_host',
    'extend',
    'restart_stage'
  ]);
  const choosing = $derived(actionFor(view, 'begin_question'));
  const presenting = $derived(room.phase !== 'finished' && isPresentationStage(view.stage.kind));
  const showBoard = $derived(isBoardStage(view.stage.kind));
  const isPlayer = $derived(players.some((p) => p.id === room.you));
  const roundAction = $derived(actionFor(view, 'begin_round'));
  function send(type: string, data: Record<string, unknown> = {}) {
    error = '';
    try {
      onsend(prepareAction(view, room.seq, type, data, captureScope(view)));
    } catch (e) {
      error = errorText(e);
    }
  }
  function name(id: string | undefined) {
    return room.participants.find((p) => p.id === id)?.name || '—';
  }
</script>

<div class="game-v2" class:live-game={room.phase !== 'lobby'}>
  {#if room.phase !== 'lobby' && room.phase !== 'finished'}<div class="row between section-head">
      <div>
        <p class="eyebrow">
          {view.round?.name || 'Лобби'} · {room.phase === 'paused'
            ? 'Пауза'
            : stageNames[view.stage.kind] || 'Игра'}
        </p>
        <h2>
          {presenting
            ? stageNames[view.stage.kind]
            : view.final
              ? 'Финал'
              : view.question
                ? questionTypes[view.question.type] || 'Вопрос'
                : 'Выбор вопроса'}
        </h2>
        <p class="helper">
          Выбирает {name(view.chooser_id)}{view.stage.turn_id
            ? ` · Сейчас ходит ${name(view.stage.turn_id)}`
            : ''}
        </p>
      </div>
      <GameTimer
        stage={view.stage}
        serverTime={view.server_time_ms}
        paused={room.phase === 'paused'}
      />
    </div>
  {/if}
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  <div class="row room-toolbar">
    <GameSounds {view} seq={room.seq} phase={room.phase} {ready} />
    <button class="small" onclick={() => toolsDialog.showModal()}>Настройки и управление</button>
  </div>
  {#if room.phase === 'paused'}<p class="notice">
      Игра на паузе. Остаток времени сохранён.
    </p>{:else if view.stage.expired && room.phase !== 'finished'}<p class="notice">
      Время этапа вышло. Ведущий может продолжить игру доступным действием.
    </p>{/if}
  {#if room.phase === 'lobby'}
    <Lobby
      {room}
      {board}
      selectedRound={round?.id || ''}
      {blocked}
      canStart={!!roundAction}
      canReady={!!actionFor(view, 'set_ready')}
      onroundchange={(id) => (selectedRound = id)}
      onstart={() => round && onround(round.id)}
      onready={(ready) => send('set_ready', { ready })}
    />
  {/if}
  <div
    class="room-grid"
    class:lobby-options={room.phase === 'lobby'}
    class:participant-view={!isHost || room.phase === 'finished'}
  >
    <div class="stack">
      {#if !view.final && !presenting && view.question && !showBoard && room.phase !== 'finished'}<QuestionMechanic
          {view}
          participants={room.participants}
          you={room.you}
        />{/if}
      {#if room.phase !== 'lobby' && room.phase !== 'finished' && showBoard}
        <div class="row between">
          {#if isHost}<label
              >Раунд<select
                value={selectedRound || view.round?.id || board?.rounds[0]?.id || ''}
                onchange={(event) => (selectedRound = event.currentTarget.value)}
                >{#each board?.rounds || [] as option}<option value={option.id}
                    >{option.name}{option.type === 'final' ? ' · финал' : ''}</option
                  >{/each}</select
              ></label
            >{#if roundAction && round && (!roundAction.targets?.length || roundAction.targets.includes(round.id))}<button
                disabled={blocked}
                onclick={() => onround(round!.id)}>Начать раунд</button
              >{/if}
          {/if}
        </div>
        {#if isHost && round && !view.final}<label class="check"
            ><input type="checkbox" bind:checked={replay} />Разрешить явный повтор сыгранного
            вопроса</label
          >{/if}
      {/if}
      {#if isHost && !publicConnected}<p class="notice" role="status">
          {publicError || 'Восстанавливаем подключение публичной сцены…'}
          <button class="small" onclick={() => publicConnection?.connect()}
            >Повторить подключение сцены</button
          >
        </p>{/if}
      {#if publicView}
        <PublicGameStage
          room={isHost && publicRoom ? publicRoom : room}
          view={publicView}
          {board}
          {roomToken}
          ready={ready && (!isHost || publicConnected)}
          blocked={blocked || (isHost && (!publicConnected || publicRoom?.seq !== room.seq))}
          {replay}
          mediaSeq={room.seq}
          mediaParticipantID={room.you}
          selectionTargets={choosing?.targets}
          onselect={choosing ? onselect : undefined}
          {onsend}
          mediaActions={publicView.stage.id === view.stage.id
            ? view.allowed_actions?.filter((a) => a.type === 'media_ready') || []
            : []}
        />
      {:else}<p class="helper" role="status">Подключаем публичную сцену…</p>{/if}
      {#if !presenting && room.phase !== 'finished'}
        {#if view.readiness?.length && isHost}<section class="notice media-readiness">
            <h3>Готовность к показу</h3>
            <div class="list">
              {#each view.readiness as participant}<div class="row between">
                  <span>{name(participant.id)}</span><span class="helper"
                    >{{
                      loading: 'Загрузка',
                      ready: 'Готов',
                      error: 'Ошибка',
                      hidden: 'Вкладка скрыта'
                    }[participant.status]}</span
                  >
                </div>{/each}
            </div>
            {#if actionFor(view, 'continue_media')}<p class="helper">
                Не все участники готовы. Продолжение запустит показ для тех, кто успел загрузить
                файл.
              </p>{/if}
          </section>{:else if view.readiness?.length && ['prepare_content', 'scheduled_content'].includes(view.stage.kind)}<p
            class="helper media-readiness"
          >
            Готовы к показу: {view.readiness.filter((p) => p.status === 'ready').length} из {view
              .readiness.length}.
          </p>{/if}
        {#if isPlayer && view.question && ['withButton', 'simple'].includes(view.question.type) && !showBoard}<GameBuzzer
            {view}
            you={room.you}
            connected={ready}
            {pending}
            paused={room.phase === 'paused'}
            onsend={() => send('buzz')}
          />{/if}
        {#if view.self.wager !== undefined || view.self.answer !== undefined}<div
            class="notice success"
          >
            <strong>Только для вас</strong>{#if view.self.wager !== undefined}<p
                style="margin:8px 0 0"
              >
                Ваша ставка: {view.self.wager}
              </p>{/if}{#if view.self.answer !== undefined}<p
                style="margin:8px 0 0;white-space:pre-wrap"
              >
                Ваш ответ: {view.self.answer}
              </p>{/if}
          </div>{/if}
        {#if !isHost}
          {#key `${view.stage.id}:${view.stage.kind}:${view.stage.instance_id || ''}`}{#each (view.allowed_actions || []).filter((action) => formTypes.has(action.type) && !administrative.has(action.type)) as action (action.type)}<GameActionForm
                {view}
                seq={room.seq}
                {action}
                participants={room.participants}
                disabled={blocked}
                {onsend}
              />{/each}{/key}
        {/if}
        {#if visibleSubmissions.length}<section class="panel stack">
            <h3>Ответы участников</h3>
            {#each visibleSubmissions as submission}<div class="notice">
                <strong>{name(submission.id)}</strong>{#if submission.answer !== undefined}<p
                    style="white-space:pre-wrap;margin:8px 0"
                  >
                    {submission.answer || 'Без текста'}
                  </p>{/if}{#if submission.wager !== undefined}<p
                    class="helper"
                    style="margin-bottom:0"
                  >
                    Ставка: {submission.wager}
                  </p>{/if}{#if submission.correct !== undefined}<p
                    class="helper"
                    style="margin-bottom:0"
                  >
                    {submission.correct ? 'Верно' : 'Неверно'}
                  </p>{/if}
              </div>{/each}
          </section>{/if}
        {#if room.phase !== 'lobby' && !showBoard && !view.question && !roundAction && !choosing && !view.final}<div
            class="empty"
          >
            <h3>Ждём продолжения</h3>
            <p>Следующее действие выполнит участник, которому передан ход, или ведущий.</p>
          </div>{/if}
      {/if}
    </div>
    {#if isHost && room.phase !== 'lobby' && room.phase !== 'finished'}<aside
        class="stack host-console"
        aria-label="Пульт ведущего"
      >
        <h3>Пульт ведущего</h3>
        {#if publicView?.stage.id === view.stage.id && view.question?.text && view.question.text !== publicView.question?.text}<section
            class="host-question-notes"
          >
            <p class="helper">Текст и реплики · только ведущему</p>
            <p style="white-space:pre-wrap;overflow-wrap:anywhere">{view.question.text}</p>
          </section>{/if}
        {#if publicView?.stage.id === view.stage.id && view.question?.media && view.question.media.id !== publicView.question?.media?.id}
          <section class="host-media">
            <p class="helper">Медиа · только ведущему</p>
            <MediaPlayer
              media={view.question.media}
              {view}
              seq={room.seq}
              you={room.you}
              roomID={room.room_id}
              token={roomToken}
              connected={ready}
              paused={room.phase === 'paused'}
              {onsend}
            />
          </section>
        {/if}
        {#if view.question?.answer !== undefined}<section class="host-answer">
            <p class="helper">Правильный ответ · только ведущему</p>
            <p>{view.question.answer}</p>
          </section>{/if}
        {#if view.stage.turn_id}<p>
            Сейчас отвечает / действует: <strong>{name(view.stage.turn_id)}</strong>
          </p>{/if}
        {#key `${view.stage.id}:${view.stage.kind}:${view.stage.instance_id || ''}`}{#each (view.allowed_actions || []).filter((action) => formTypes.has(action.type) && !administrative.has(action.type)) as action (action.type)}<GameActionForm
              {view}
              seq={room.seq}
              {action}
              participants={room.participants}
              disabled={blocked}
              {onsend}
            />{/each}{/key}
        <div class="row game-controls">
          {#each [{ type: 'skip_presentation', label: 'Пропустить заставку' }, { type: 'pause', label: 'Пауза Ⅱ' }, { type: 'resume', label: 'Продолжить ▶' }, { type: 'advance', label: view.stage.kind === 'present_answer' ? 'Следующий фрагмент ответа' : view.stage.kind === 'collect_answer' ? 'Ответ прозвучал' : view.stage.kind === 'collect_answers' ? 'Закрыть сбор ответов' : 'Продолжить →' }, { type: 'wait_media', label: 'Ждать ещё 10 секунд' }, { type: 'continue_media', label: 'Продолжить без всех' }] as control}{#if actionFor(view, control.type) && !(control.type === 'advance' && view.stage.kind === 'collect_answer')}<button
                disabled={blocked}
                onclick={() => send(control.type)}>{control.label}</button
              >{/if}{/each}{#if actionFor(view, 'finish')}{#if finishConfirm}<span
                >Завершить игру с текущим счётом?</span
              ><button class="danger" disabled={blocked} onclick={() => send('finish')}
                >Завершить</button
              ><button onclick={() => (finishConfirm = false)}>Отмена</button>{:else}<button
                class="ghost"
                onclick={() => (finishConfirm = true)}>Завершить игру</button
              >{/if}{/if}
        </div>
      </aside>{/if}
  </div>
  <PublicGameScores {room} {view} />
  <dialog class="game-tools" bind:this={toolsDialog} aria-label="Настройки и управление">
    <div class="row between">
      <h2>Настройки и управление</h2>
      <button class="small" onclick={() => toolsDialog.close()}>Закрыть</button>
    </div>
    <div class="stack">
      <RoomParticipants
        {room}
        {view}
        token={roomToken}
        disabled={blocked}
        {onsend}
        {onunauthorized}
      />
      <RoomPreferences {room} {view} {ready} {pending} {onsend} />
      {#if (view.allowed_actions || []).some((action) => administrative.has(action.type))}<details>
          <summary>Управление и апелляции</summary
          >{#key `${view.stage.id}:${view.stage.kind}:${view.stage.instance_id || ''}`}{#each (view.allowed_actions || []).filter( (action) => administrative.has(action.type) ) as action (action.type)}<GameActionForm
                {view}
                seq={room.seq}
                {action}
                participants={room.participants}
                disabled={blocked}
                {onsend}
              />{/each}{/key}
        </details>{/if}
      <details>
        <summary>Последние решения</summary>
        <div class="list" style="margin-top:16px">
          {#each view.decisions || [] as decision}<div class="notice">
              <strong
                >{name(decision.participant_id)} · {decision.delta > 0
                  ? '+'
                  : ''}{decision.delta}</strong
              >
              <p class="helper" style="margin:6px 0 0">
                {decision.correct ? 'Верно' : 'Неверно'}{decision.reason
                  ? ` · ${decision.reason}`
                  : ''}
              </p>
            </div>{:else}<p class="helper">
              Решений пока нет.
            </p>{/each}{#each view.appeals || [] as appeal}<div class="notice">
              <strong>Апелляция: {name(appeal.participant_id)}</strong>
              <p>{appeal.reason}</p>
              <p class="helper">
                {appeal.resolved ? `Рассмотрена: ${appeal.resolution || ''}` : 'Ожидает решения'}
              </p>
            </div>{/each}
        </div>
      </details>
      <button class="small" onclick={onresults}>Журнал и результаты</button>
    </div>
  </dialog>
</div>

<style>
  .room-grid.lobby-options,
  .room-grid.participant-view {
    display: block;
  }
  .room-toolbar {
    justify-content: flex-end;
    margin-bottom: 12px;
  }
  .host-console {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--card-radius, 2px);
    padding: 20px;
    align-self: start;
  }
  .host-console h3 {
    margin: 0;
    font-size: 18px;
  }
  .host-answer {
    border-left: 3px solid var(--accent);
    padding-left: 14px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .host-answer p {
    margin: 6px 0;
  }
  .host-console .game-controls {
    display: flex;
    flex-direction: column;
    align-items: stretch;
  }
  .game-tools {
    position: fixed;
    inset: 0 0 0 auto;
    margin: 0;
    width: min(540px, 100%);
    height: 100dvh;
    max-height: 100dvh;
    max-width: 100vw;
    overflow-y: auto;
    padding: 24px;
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--line);
    border-radius: 0;
  }
  .game-tools::backdrop {
    background: #0009;
  }
  .game-tools > .row {
    margin-bottom: 20px;
  }
  @media (min-width: 1001px) {
    .room-grid:not(.lobby-options):not(.participant-view) {
      grid-template-columns: minmax(0, 1fr) 310px;
      gap: 20px;
    }
  }
</style>
