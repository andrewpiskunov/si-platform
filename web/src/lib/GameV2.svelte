<script lang="ts">
  import {
    actionFor,
    captureScope,
    prepareAction,
    stageNames,
    questionTypes,
    classicRules,
    type SessionView,
    type V2Command,
    type Rules
  } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  import type { Board } from './types.ts';
  import GameActionForm from './GameActionForm.svelte';
  import GameRules from './GameRules.svelte';
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
    onresults
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
  } = $props();
  let error = $state(''),
    selectedRound = $state(''),
    finishConfirm = $state(false),
    replay = $state(false),
    rules = $state<Rules>(classicRules()),
    rulesVersion = 0;
  $effect(() => {
    if (rulesVersion !== view.rules_version) {
      rules = { ...view.rules };
      rulesVersion = view.rules_version;
    }
  });
  const round = $derived(
    board?.rounds.find((item) => item.id === (selectedRound || view.round?.id)) || board?.rounds[0]
  );
  const players = $derived(room.participants.filter((p) => p.role === 'player'));
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
  <div class="row between section-head">
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
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  <div class="row game-controls">
    {#each [{ type: 'pause', label: 'Пауза Ⅱ' }, { type: 'resume', label: 'Продолжить ▶' }, { type: 'advance', label: view.stage.kind === 'present_answer' ? 'Следующий фрагмент ответа' : view.stage.kind === 'collect_answer' ? 'Ответ прозвучал' : view.stage.kind === 'collect_answers' ? 'Закрыть сбор ответов' : 'Продолжить →' }, { type: 'open_buzzer', label: 'Открыть кнопку' }, { type: 'wait_media', label: 'Ждать ещё 10 секунд' }, { type: 'continue_media', label: 'Продолжить без всех' }] as control}{#if actionFor(view, control.type)}<button
          disabled={blocked}
          onclick={() => send(control.type)}>{control.label}</button
        >{/if}{/each}{#if actionFor(view, 'finish')}{#if finishConfirm}<span
          >Завершить игру с текущим счётом?</span
        ><button class="danger" disabled={blocked} onclick={() => send('finish')}>Завершить</button
        ><button onclick={() => (finishConfirm = false)}>Отмена</button>{:else}<button
          class="ghost"
          onclick={() => (finishConfirm = true)}>Завершить игру</button
        >{/if}{/if}
  </div>
  {#if room.phase === 'paused'}<p class="notice">
      Игра на паузе. Остаток времени сохранён.
    </p>{:else if view.stage.expired}<p class="notice">
      Время этапа вышло. Ведущий может продолжить игру доступным действием.
    </p>{/if}
  {#if room.phase === 'lobby'}<SoundCheck />{/if}
  <div class="room-grid">
    <div class="stack">
      {#if roundAction || choosing}<section class="stack">
          <div class="row between">
            <label
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
          </div>
          {#if choosing && round}<div class="board-scroll">
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
            <label class="check"
              ><input type="checkbox" bind:checked={replay} />Разрешить явный повтор сыгранного
              вопроса</label
            >{/if}
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
      {#if view.question}<div class="question-stage" class:media-question={!!view.question.media}>
          {#if !(view.question.type === 'secret' && view.stage.kind === 'select_recipient')}
            <p class="question-price">
              {view.question.theme}{view.question.theme ? ' · ' : ''}{view.question.price} очков
            </p>{/if}
          {#if view.question.text}<h2>{view.question.text}</h2>{:else if !view.question.media}<h2>
              {stageNames[view.stage.kind] || 'Готовимся к вопросу'}
            </h2>{/if}{#if view.question.content_total > 1}<p class="helper">
              Фрагмент {view.question.content_index + 1} из {view.question.content_total}
            </p>{/if}{#if view.question.answer !== undefined}<div
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
      {#if actionFor(view, 'buzz')}<div>
          <button class="buzz" disabled={blocked} onclick={() => send('buzz')}>Знаю ответ!</button>
          <p class="helper" style="text-align:center;margin-top:12px">
            Кнопка или пробел{view.stage.kind === 'present_question'
              ? ' · Вопрос ещё читается: раннее нажатие может привести к фальстарту.'
              : ''}
          </p>
        </div>{/if}
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
      {#key `${view.stage.id}:${view.stage.kind}:${view.stage.instance_id || ''}`}{#each (view.allowed_actions || []).filter((action) => formTypes.has(action.type) && !administrative.has(action.type)) as action (action.type)}<GameActionForm
            {view}
            seq={room.seq}
            {action}
            participants={room.participants}
            disabled={blocked}
            {onsend}
          />{/each}{/key}
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
      {#if !view.question && !roundAction && !choosing && !view.final}<div class="empty">
          <h3>{room.phase === 'finished' ? 'Спасибо за игру' : 'Ждём продолжения'}</h3>
          <p>
            {room.phase === 'finished'
              ? 'Итоговый счёт сохранён.'
              : 'Следующее действие выполнит участник, которому передан ход, или ведущий.'}
          </p>
        </div>{/if}
    </div>
    <aside class="stack">
      <div>
        <h3>Игроки · {players.length}/12</h3>
        <div class="score-list">
          {#each room.phase === 'finished' ? [...players].sort((a, b) => b.score - a.score) : players as participant}{@const status =
              view.players?.find((p) => p.id === participant.id)}
            <div class="score-item" class:winner={view.stage.turn_id === participant.id}>
              <div>
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
        <p class="helper" style="margin-top:16px">
          Ведущий: {name(room.participants.find((p) => p.role === 'host')?.id)}
        </p>
      </div>
      {#if actionFor(view, 'set_rules')}<details>
          <summary>Правила игры</summary>
          <form
            class="stack"
            onsubmit={(event) => {
              event.preventDefault();
              send('set_rules', { rules });
            }}
          >
            <GameRules {rules} onchange={(value) => (rules = value)} disabled={blocked} /><button
              disabled={blocked}>Применить правила</button
            >
          </form>
        </details>{/if}
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
    </aside>
  </div>
</div>
