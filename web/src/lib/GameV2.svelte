<script lang="ts">
  import Avatar from './Avatar.svelte';
  import GameBuzzer from './GameBuzzer.svelte';
  import Lobby from './Lobby.svelte';
  import {
    actionFor,
    isBoardStage,
    captureScope,
    prepareAction,
    stageNames,
    questionTypes,
    type SessionView,
    type V2Command
  } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  import type { Board } from './types.ts';
  import RoomParticipants from './RoomParticipants.svelte';
  import GameActionForm from './GameActionForm.svelte';
  import RoomPreferences from './RoomPreferences.svelte';
  import MediaPlayer from './MediaPlayer.svelte';
  import SoundCheck from './SoundCheck.svelte';
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
  const host = $derived(room.participants.find((p) => p.role === 'host' && !p.removed));
  const isHost = $derived(room.participants.some((p) => p.id === room.you && p.role === 'host'));
  const blocked = $derived(!ready || pending > 0);
  const visibleSubmissions = $derived(
    (view.submissions || []).filter(
      (item) => item.answer !== undefined || item.wager !== undefined || item.correct !== undefined
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
  function questionAllowed(id: string) {
    return !!choosing && (!choosing.targets?.length || choosing.targets.includes(id));
  }
</script>

<div class="game-v2">
  {#if room.phase !== 'lobby'}<div class="row between section-head">
      <div>
        <p class="eyebrow">
          {view.round?.name || 'Лобби'} · {room.phase === 'paused'
            ? 'Пауза'
            : stageNames[view.stage.kind] || 'Игра'}
        </p>
        <h2>
          {room.phase === 'finished'
            ? 'Итоги игры'
            : view.question
              ? questionTypes[view.question.type] || 'Вопрос'
              : 'Готовы сыграть?'}
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
    <button class="small" onclick={() => toolsDialog.showModal()}>Настройки и управление</button>
  </div>
  {#if room.phase === 'paused'}<p class="notice">
      Игра на паузе. Остаток времени сохранён.
    </p>{:else if view.stage.expired}<p class="notice">
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
    <SoundCheck />
  {/if}
  <div
    class="room-grid"
    class:lobby-options={room.phase === 'lobby'}
    class:participant-view={!isHost}
  >
    <div class="stack">
      {#if !isHost && view.stage.kind === 'resolved' && view.question?.answer !== undefined}<section class="notice revealed-answer"><h3>Правильный ответ</h3><p>{view.question.answer}</p></section>{/if}
      {#if room.phase !== 'lobby' && showBoard}<section class="stack">
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
          {#if round && showBoard}<div class="board-scroll">
              <div class="board">
                {#each round.themes as theme}<div
                    class="board-row"
                    style={`--columns:${Math.max(1, theme.questions.length)}`}
                  >
                    <div class="board-theme">{theme.name}</div>
                    {#each theme.questions as question}<button
                        class="board-cell"
                        disabled={blocked ||
                          !questionAllowed(question.id) ||
                          (question.played && !replay)}
                        onclick={() => onselect(question.id, question.played && replay)}
                        aria-label={`${theme.name}, ${question.value} очков${question.played ? ', сыгран' : ''}`}
                        >{question.played && !replay ? '—' : question.value}</button
                      >{/each}
                  </div>{/each}
              </div>
            </div>
            {#if isHost}<label class="check"
                ><input type="checkbox" bind:checked={replay} />Разрешить явный повтор сыгранного
                вопроса</label
              >{/if}{/if}
        </section>{/if}
      {#if view.final}<section class="panel">
          <h3>Темы финала</h3>
          <div class="row">
            {#each view.final.themes || [] as theme}<span class="pill">{theme.name}</span>{/each}
          </div>
          <p class="helper" style="margin-top:16px">
            Участвуют: {(view.final.eligible_ids || []).map(name).join(', ') || '—'}
          </p>
        </section>{/if}
      {#if view.question && !showBoard}<div
          class="question-stage"
          class:buzzer-open={view.stage.kind === 'buzz_open' && room.phase !== 'paused'}
          class:media-question={!!view.question.media}
        >
          {#if !(view.question.type === 'secret' && view.stage.kind === 'select_recipient')}
            <p class="question-price">
              {view.question.theme}{view.question.theme ? ' · ' : ''}{view.question.price} очков
            </p>{/if}
          {#if view.question.text}<h2>{view.question.text}</h2>{:else if !view.question.media}<h2>
              {stageNames[view.stage.kind] || 'Готовимся к вопросу'}
            </h2>{/if}{#if view.question.content_total > 1}<p class="helper">
              Фрагмент {view.question.content_index + 1} из {view.question.content_total}
            </p>{/if}{#if !isHost && view.question.answer !== undefined}<div
              class="notice"
              style="margin-top:20px;width:100%;text-align:left"
            >
              <strong
                >Ответ{view.stage.kind === 'present_answer' && view.question.answer_total
                  ? ` · пояснение ${(view.question.answer_index ?? 0) + 1} из ${view.question.answer_total}`
                  : ''}:</strong
              >
              <p style="margin:8px 0 0;white-space:pre-wrap">{view.question.answer}</p>
            </div>{/if}
        </div>{/if}
      {#if view.question?.media}<MediaPlayer
          media={view.question.media}
          {view}
          seq={room.seq}
          you={room.you}
          roomID={room.room_id}
          token={roomToken}
          connected={ready}
          paused={room.phase === 'paused'}
          {onsend}
        />{/if}
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
              Не все участники готовы. Продолжение запустит показ для тех, кто успел загрузить файл.
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
      {#if view.auction}<div class="notice">
          <strong
            >Текущая ставка: {view.auction.amount}{view.auction.all_in ? ' · ва-банк' : ''}</strong
          >
          <p style="margin-bottom:0">Лидер: {name(view.auction.leader_id)}</p>
        </div>{/if}
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
          <h3>{room.phase === 'finished' ? 'Спасибо за игру' : 'Ждём продолжения'}</h3>
          <p>
            {room.phase === 'finished'
              ? 'Итоговый счёт сохранён.'
              : 'Следующее действие выполнит участник, которому передан ход, или ведущий.'}
          </p>
        </div>{/if}
    </div>
    {#if isHost && room.phase !== 'lobby'}<aside
        class="stack host-console"
        aria-label="Пульт ведущего"
      >
        <h3>Пульт ведущего</h3>
        {#if view.question?.answer !== undefined}<section class="host-answer">
            <p class="helper">Правильный ответ · только ведущему</p>
            <p>{view.question.answer}</p>
          </section>{/if}
        {#if view.stage.turn_id}<p>
            Сейчас отвечает / действует: <strong>{name(view.stage.turn_id)}</strong>
          </p>{/if}
        <div class="row game-controls">
          {#each [{ type: 'pause', label: 'Пауза Ⅱ' }, { type: 'resume', label: 'Продолжить ▶' }, { type: 'advance', label: view.stage.kind === 'present_answer' ? 'Следующий фрагмент ответа' : view.stage.kind === 'collect_answer' ? 'Ответ прозвучал' : view.stage.kind === 'collect_answers' ? 'Закрыть сбор ответов' : 'Продолжить →' }, { type: 'open_buzzer', label: 'Открыть кнопку' }, { type: 'wait_media', label: 'Ждать ещё 10 секунд' }, { type: 'continue_media', label: 'Продолжить без всех' }] as control}{#if actionFor(view, control.type)}<button
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
        {#key `${view.stage.id}:${view.stage.kind}:${view.stage.instance_id || ''}`}{#each (view.allowed_actions || []).filter((action) => formTypes.has(action.type) && !administrative.has(action.type)) as action (action.type)}<GameActionForm
              {view}
              seq={room.seq}
              {action}
              participants={room.participants}
              disabled={blocked}
              {onsend}
            />{/each}{/key}
      </aside>{/if}
  </div>
  <section class="participants-strip" aria-label="Счёт игроков">
    {#if room.phase !== 'lobby'}<div>
        <h3>Игроки · {players.length}/12</h3>
        <div class="score-list">
          {#each room.phase === 'finished' ? [...players].sort((a, b) => b.score - a.score) : players as participant}{@const status =
              view.players?.find((p) => p.id === participant.id)}
            <div class="score-item" class:winner={view.stage.turn_id === participant.id}>
              <Avatar identity={participant.id} />
              <div class="participant-name">
                {participant.name}{participant.id === room.you ? ' (вы)' : ''}
                <div class="helper">
                  {status?.wager_submitted ? 'Ставка принята · ' : ''}{status?.answer_submitted
                    ? 'Ответ принят'
                    : status?.attempted
                      ? 'Попытка использована'
                      : ''}{status?.judged ? ' · Оценён' : ''}
                </div>
              </div>
              <strong>{participant.score}</strong>
            </div>{/each}
        </div>
        {#if host}<div class="host-identity">
            <Avatar identity={host.id} small />
            <div>
              <span class="helper">Ведущий</span>
              <div>{host.name}</div>
            </div>
          </div>{/if}
      </div>
    {/if}
  </section>
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
    border-radius: 12px;
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
  .question-stage {
    min-height: clamp(340px, 51vh, 650px);
    border: 2px solid var(--line);
  }
  .question-stage.buzzer-open {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent);
  }
  .question-stage h2 {
    font-size: clamp(27px, 3.2vw, 54px);
    line-height: 1.3;
    text-wrap: balance;
  }
  .participants-strip {
    margin-top: 20px;
  }
  .participants-strip :global(.score-list) {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 10px;
  }
  .participants-strip :global(.score-item) {
    min-width: 0;
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
  @media (min-width: 1300px) {
    .participants-strip :global(.score-list:has(.score-item:nth-child(4))) {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }
  @media (max-width: 700px) {
    .participants-strip :global(.score-list) {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .question-stage {
      min-height: 300px;
    }
  }
  @media (prefers-reduced-motion: no-preference) {
    .question-stage {
      animation: question-in 0.3s ease-out;
    }
    @keyframes question-in {
      from {
        opacity: 0;
        transform: translateY(8px);
      }
      to {
        opacity: 1;
        transform: none;
      }
    }
  }
</style>
