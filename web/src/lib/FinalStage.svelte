<script lang="ts">
  import type { SessionView } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  import { playerDecision } from './game-feedback.ts';
  import FinalResults from './FinalResults.svelte';
  let { room, view }: { room: RoomState; view: SessionView } = $props();
  const steps = ['Темы', 'Ставки', 'Вопрос', 'Ответы и оценка', 'Итоги'];
  const eligible = $derived(view.final?.eligible_ids || []);
  const statuses = $derived((view.players || []).filter((p) => eligible.includes(p.id)));
  const revealed = $derived((view.submissions || []).filter((s) => s.revealed));
  const step = $derived(
    room.phase === 'finished'
      ? 4
      : ['eliminate_theme', 'select_final'].includes(view.stage.kind) || !view.question
        ? 0
        : view.stage.kind === 'collect_wagers'
          ? 1
          : ['reveal', 'judge', 'present_answer', 'resolved'].includes(view.stage.kind)
            ? 3
            : 2
  );
  const complete = $derived(view.stage.kind === 'resolved');
  function name(id: string | undefined) {
    return room.participants.find((p) => p.id === id)?.name || '—';
  }
</script>

<section
  class="final-stage"
  data-testid="final-stage"
  data-stage={view.stage.kind}
  aria-label="Финал"
>
  <ol class="progress" aria-label="Этапы финала">
    {#each steps as label, index}<li
        class:current={index === step}
        class:past={index < step}
        aria-current={index === step ? 'step' : undefined}
      >
        <span>{index + 1}</span>{label}
      </li>{/each}
  </ol>
  {#if room.phase === 'finished'}
    <FinalResults {room} />
  {:else}
    <div class="final-heading">
      <p class="eyebrow">Финал · {steps[step]}</p>
      <h3>
        {!eligible.length
          ? 'Нет игроков с положительным счётом'
          : complete
            ? 'Финальный вопрос завершён'
            : view.stage.kind === 'eliminate_theme'
              ? 'Какая тема уйдёт?'
              : view.stage.kind === 'select_final'
                ? 'Тема финала выбрана'
                : view.stage.kind === 'collect_wagers'
                  ? 'Закрытые ставки'
                  : view.stage.kind === 'reveal'
                    ? 'Раскрываем ответы по очереди'
                    : view.stage.kind === 'judge'
                      ? 'Решение ведущего'
                      : 'Финальный вопрос'}
      </h3>
      {#if !eligible.length}<p>
          Участвовать в финале можно со счётом выше нуля. Ведущий может завершить игру; все игроки
          останутся в итоговой таблице.
        </p>
      {:else if complete}<p>Ведущий может завершить игру, чтобы показать итоговые места.</p>
      {:else if view.stage.turn_id && ['eliminate_theme', 'reveal', 'judge', 'present_answer'].includes(view.stage.kind)}<p
          class="active-player"
        >
          {view.stage.kind === 'eliminate_theme'
            ? 'Убирает тему'
            : 'Сейчас раскрываем и оцениваем'}: <strong>{name(view.stage.turn_id)}</strong>
        </p>
      {:else if view.stage.kind === 'select_final'}<p>
          Ведущий запускает оставшийся вопрос. Затем участники делают ставки.
        </p>
      {:else if view.stage.kind === 'collect_wagers'}<p>
          Ставки приняты: <strong
            >{statuses.filter((p) => p.wager_submitted).length} из {eligible.length}</strong
          >. Суммы скрыты до раскрытия.
        </p>
      {:else if view.stage.kind === 'collect_answers'}<p>
          {view.rules.answer_mode === 'oral'
            ? 'Обдумайте ответ. Ведущий выслушает и оценит каждого по очереди.'
            : `Ответы приняты: ${statuses.filter((p) => p.answer_submitted).length} из ${eligible.length}. Тексты скрыты до раскрытия.`}
        </p>
      {:else}<p>Участвуют: {eligible.map(name).join(' · ')}</p>{/if}
      {#if eligible.length && step === 3}<p class="helper">
          Оценены: {statuses.filter((p) => p.judged).length} из {eligible.length}
        </p>{/if}
    </div>
    {#if step === 0 && eligible.length}<div class="themes" aria-label="Оставшиеся темы финала">
        {#each view.final?.themes || [] as theme}<div class="theme">{theme.name}</div>{/each}
      </div>{/if}
    {#if revealed.length}<div class="revealed-results" aria-label="Раскрытые ответы финала">
        {#each revealed as submission (submission.id)}{@const decision = playerDecision(
            view,
            submission.id
          )}
          <article
            class="result"
            class:active={submission.id === view.stage.turn_id && !complete}
            data-testid="final-player-result"
            data-player-id={submission.id}
          >
            <div class="row between">
              <strong>{name(submission.id)}</strong><span
                >{submission.correct === undefined
                  ? 'Ожидает оценки'
                  : submission.correct
                    ? 'Верно'
                    : 'Неверно'}</span
              >
            </div>
            {#if submission.answer !== undefined}<p class="answer">
                {submission.answer || 'Без текста'}
              </p>{:else if view.rules.answer_mode === 'oral'}<p class="helper">
                Устный ответ
              </p>{/if}
            <p class="helper">
              {#if submission.wager !== undefined}Ставка: {submission.wager}{/if}{#if decision}
                · Изменение счёта: {decision.delta > 0 ? '+' : ''}{decision.delta}{/if}
            </p>
          </article>
        {/each}
      </div>{/if}
  {/if}
</section>

<style>
  .final-stage {
    min-width: 0;
  }
  .progress {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 16px;
    padding: 0;
    margin: 0 0 20px;
    list-style: none;
  }
  .progress li {
    display: flex;
    gap: 7px;
    align-items: center;
    font-size: 13px;
    color: var(--muted);
  }
  .progress span {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border: 1px solid var(--line);
    border-radius: 50%;
  }
  .progress .current {
    color: var(--accent);
    font-weight: 700;
  }
  .current span {
    border-color: var(--accent);
  }
  .past span {
    background: color-mix(in srgb, var(--accent) 15%, transparent);
  }
  .final-heading {
    padding: 20px;
    border-left: 4px solid var(--accent);
    background: color-mix(in srgb, var(--accent) 6%, transparent);
  }
  h3 {
    font-size: clamp(22px, 2.5vw, 34px);
    margin: 0;
  }
  p {
    margin: 10px 0 0;
    overflow-wrap: anywhere;
  }
  .eyebrow {
    margin: 0 0 8px;
  }
  .active-player {
    font-size: 20px;
  }
  .themes,
  .revealed-results {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(220px, 100%), 1fr));
    gap: 12px;
    margin-top: 16px;
  }
  .theme {
    padding: 22px;
    border: 1px solid var(--accent);
    border-radius: 12px;
    font-size: 22px;
    font-weight: 700;
    overflow-wrap: anywhere;
  }
  .result {
    padding: 16px;
    border: 1px solid var(--line);
    border-radius: 12px;
    min-width: 0;
  }
  .result.active {
    border-color: var(--accent);
  }
  .answer {
    white-space: pre-wrap;
  }
</style>
