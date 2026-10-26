<script lang="ts">
  import FinalResults from './FinalResults.svelte';
  import FinalStage from './FinalStage.svelte';
  import AnswerFeedback from './AnswerFeedback.svelte';
  import PresentationScene from './PresentationScene.svelte';
  import QuestionText from './QuestionText.svelte';
  import MediaPlayer from './MediaPlayer.svelte';
  import {
    isBoardStage,
    isPresentationStage,
    stageNames,
    type SessionView,
    type V2Command,
    type AllowedAction
  } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  import type { Board } from './types.ts';
  // Callers must supply a spectator projection, never a host SessionView.
  // Only media readiness and board selection are delegated to the room controller.
  let {
    room,
    view,
    board,
    roomToken,
    ready,
    blocked = true,
    replay = false,
    selectionTargets,
    onselect,
    onsend = () => {},
    mediaActions = [],
    mediaSeq = room.seq,
    mediaParticipantID = room.you
  }: {
    room: RoomState;
    view: SessionView;
    board: Board | null;
    roomToken: string;
    ready: boolean;
    blocked?: boolean;
    replay?: boolean;
    selectionTargets?: string[] | null;
    onselect?: (id: string, replay?: boolean) => void;
    onsend?: (command: V2Command) => void;
    mediaActions?: AllowedAction[];
    mediaSeq?: number;
    mediaParticipantID?: string;
  } = $props();
  const round = $derived(board?.rounds.find((r) => r.id === view.round?.id) || board?.rounds[0]);
  const presenting = $derived(room.phase !== 'finished' && isPresentationStage(view.stage.kind));
  const showBoard = $derived(isBoardStage(view.stage.kind));
  const mediaView = $derived({
    ...view,
    self: {},
    allowed_actions: mediaActions.filter((a) => a.type === 'media_ready')
  });
  const visibleSubmissions = $derived(
    (view.submissions || []).filter(
      (s) =>
        s.revealed && (s.answer !== undefined || s.wager !== undefined || s.correct !== undefined)
    )
  );
  function name(id: string | undefined) {
    return room.participants.find((p) => p.id === id)?.name || '—';
  }
  function questionAllowed(id: string) {
    return !!onselect && (!selectionTargets?.length || selectionTargets.includes(id));
  }
</script>

<div class="public-game-stage" data-testid="public-game-stage" data-stage={view.stage.kind}>
  {#if room.participants.some((p) => p.id === room.you && p.role === 'host')}
    <p role="alert">Для публичной сцены требуется зрительская проекция.</p>
  {:else if room.phase === 'finished'}
    {#if view.final}<FinalStage {room} {view} />{:else}<FinalResults {room} />{/if}
  {:else if presenting}
    <PresentationScene
      stageID={view.stage.id}
      kind={view.stage.kind}
      packName={board?.pack_name}
      roundName={view.round?.name}
      themes={round?.themes}
      questionType={view.question?.type}
      price={view.question?.price}
      theme={view.question?.theme}
      paused={room.phase === 'paused'}
    />
  {:else}
    {#if view.final}<FinalStage {room} {view} />{:else}<AnswerFeedback
        {view}
        participants={room.participants}
      />{/if}
    {#if view.stage.kind === 'resolved' && view.question?.answer !== undefined}
      <section class="notice revealed-answer">
        <h3>Правильный ответ</h3>
        <p>{view.question.answer}</p>
      </section>
    {/if}
    {#if room.phase !== 'lobby' && showBoard && (!view.final || view.stage.kind === 'select_final')}
      {#if round}<div class="board-scroll">
          <div class="board">
            {#each round.themes.filter((theme) => !view.final || view.final.themes.some((remaining) => remaining.id === theme.id)) as theme}<div
                class="board-row"
                style={`--columns:${Math.max(1, theme.questions.length)}`}
              >
                <div class="board-theme">{theme.name}</div>
                {#each theme.questions as question}<button
                    class="board-cell"
                    disabled={blocked ||
                      !questionAllowed(question.id) ||
                      (question.played && !replay)}
                    onclick={() => onselect?.(question.id, question.played && replay)}
                    aria-label={`${theme.name}, ${question.value} очков${question.played ? ', сыгран' : ''}`}
                    >{question.played && !replay ? '—' : question.value}</button
                  >{/each}
              </div>{/each}
          </div>
        </div>
      {/if}
    {/if}
    {#if view.question && !showBoard}<div
        class="question-stage"
        class:buzzer-open={view.stage.kind === 'buzz_open' && room.phase !== 'paused'}
        class:media-question={!!view.question.media}
      >
        {#if !(view.question.type === 'secret' && view.stage.kind === 'select_recipient')}
          <p class="question-price">
            {view.question.theme}{view.question.theme ? ' · ' : ''}{view.question.price} очков
          </p>{/if}
        {#if view.question.text}<QuestionText
            text={view.question.text}
            reveal={view.question.reveal}
            serverTime={view.server_time_ms}
            paused={room.phase === 'paused'}
          />{:else if !view.question.media}<h2>
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
        view={mediaView}
        seq={mediaSeq}
        you={mediaParticipantID}
        roomID={room.room_id}
        token={roomToken}
        connected={ready}
        paused={room.phase === 'paused'}
        {onsend}
      />{/if}
    {#if visibleSubmissions.length && !view.final}<section class="panel stack">
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
  {/if}
</div>

<style>
  .public-game-stage {
    display: contents;
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
  @media (max-width: 700px) {
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
