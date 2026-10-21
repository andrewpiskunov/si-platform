<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from './api.ts';
  import type { Participant } from './room-client.ts';
  import type { Decision, Appeal, Rules } from './game-v2.ts';
  import RulesSummary from './RulesSummary.svelte';
  type Results = {
    room_id: string;
    seq: number;
    phase: string;
    participants: Participant[];
    items: {
      seq: number;
      kind: string;
      actor_id: string;
      created_at: string;
      decisions: Decision[] | null;
      appeals?: Appeal[];
      removed_ids?: string[];
      chooser_change?: { from: string; to: string };
      rules_change?: { version: number; rules: Rules };
      changes: { participant_id: string; delta: number; score: number }[] | null;
    }[];
    next_after_seq?: number;
  };
  let {
    roomID,
    token,
    onclose,
    onunauthorized
  }: { roomID: string; token: string; onclose: () => void; onunauthorized: () => void } = $props();
  let entries = $state<Results['items']>([]),
    participants = $state<Participant[]>([]),
    next = $state<number | undefined>(),
    busy = $state(false),
    error = $state('');
  const names: Record<string, string> = {
    remove_participant: 'Удаление участника',
    judge: 'Оценка ответа',
    correct_judgement: 'Исправление решения',
    adjust_score: 'Изменение счёта',
    appeal: 'Апелляция',
    resolve_appeal: 'Рассмотрение апелляции',
    transfer_host: 'Передача роли ведущего',
    finish: 'Игра завершена',
    begin_round: 'Начало раунда',
    begin_question: 'Выбор вопроса',
    reveal: 'Раскрытие',
    pause: 'Пауза',
    resume: 'Продолжение',
    extend: 'Продление времени',
    restart_stage: 'Перезапуск этапа',
    set_rules: 'Настройка правил',
    set_room_settings: 'Настройки комнаты',
    advance: 'Следующий этап',
    timeout: 'Время вышло',
    answer: 'Ответ принят',
    wager: 'Ставка принята',
    bid: 'Ход аукциона',
    buzz: 'Нажатие кнопки',
    open_buzzer: 'Кнопка открыта',
    select_recipient: 'Выбор получателя',
    select_price: 'Выбор стоимости',
    eliminate_theme: 'Исключение темы'
  };
  function name(id: string) {
    return participants.find((p) => p.id === id)?.name || 'Система';
  }
  async function load(after = 0) {
    busy = true;
    error = '';
    try {
      const result = await api<Results>(
        `/rooms/${roomID}/results?after_seq=${after}&limit=100`,
        'GET',
        undefined,
        token
      );
      participants = after
        ? [...new Map([...participants, ...result.participants].map((p) => [p.id, p])).values()]
        : result.participants;
      entries = after ? [...entries, ...result.items] : result.items;
      next = result.next_after_seq;
    } catch (e) {
      if (e instanceof Error && e.message === 'unauthorized') onunauthorized();
      else error = errorText(e);
    } finally {
      busy = false;
    }
  }
  onMount(() => {
    void load();
  });
</script>

<section class="panel stack results-panel" aria-label="Журнал и результаты">
  <div class="row between">
    <h2>Журнал и результаты</h2>
    <button class="small" onclick={onclose}>Закрыть</button>
  </div>
  <div class="score-list">
    {#each [...participants]
      .filter((p) => p.role === 'player')
      .sort((a, b) => b.score - a.score) as participant}<div class="score-item">
        <span>{participant.name}{participant.removed ? ' · удалён' : ''}</span><strong
          >{participant.score}</strong
        >
      </div>{/each}
  </div>
  <div class="results-list">
    {#each entries as entry (entry.seq)}<article class="result-entry">
        <p class="helper">
          № {entry.seq} · {new Date(entry.created_at).toLocaleString('ru-RU')} · {name(
            entry.actor_id
          )}
        </p>
        <strong>{names[entry.kind] || 'Изменение состояния игры'}</strong
        >{#if entry.removed_ids?.length}<p>
            Удалены: {entry.removed_ids.map(name).join(', ')}
          </p>{/if}{#if entry.chooser_change}<p>
            Право выбора: {name(entry.chooser_change.from)} → {name(entry.chooser_change.to)}
          </p>{/if}{#if entry.rules_change}<details>
            <summary>Правила · версия {entry.rules_change.version}</summary>
            <RulesSummary rules={entry.rules_change.rules} />
          </details>{/if}{#each entry.decisions || [] as decision}<p>
            {name(decision.participant_id)} — {decision.correct
              ? 'верно'
              : 'неверно'}{decision.supersedes ? ' (исправление)' : ''}{decision.reason
              ? `: ${decision.reason}`
              : ''}
          </p>{/each}{#each entry.appeals || [] as appeal}<p>
            {name(appeal.participant_id)}: {appeal.reason}
          </p>
          {#if appeal.resolved}<p class="helper">
              Решение по апелляции: {appeal.resolution}
            </p>{/if}{/each}{#each entry.changes || [] as change}<p class="helper">
            {name(change.participant_id)}: {change.delta > 0 ? '+' : ''}{change.delta} → {change.score}
          </p>{/each}
      </article>{:else}{#if !busy}<p class="helper">Записей пока нет.</p>{/if}{/each}
  </div>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <div class="row">
    <button class="small" disabled={busy} onclick={() => load()}>Обновить</button
    >{#if next !== undefined}<button disabled={busy} onclick={() => load(next)}>Ещё записи</button
      >{/if}{#if busy}<span role="status">Загружаем…</span>{/if}
  </div>
</section>
