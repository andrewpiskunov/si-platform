<script lang="ts">
  import { untrack } from 'svelte';
  import {
    captureScope,
    prepareAction,
    utf8Length,
    answerBytes,
    type SessionView,
    type AllowedAction,
    type V2Command
  } from './game-v2.ts';
  import type { Participant } from './room-client.ts';
  import { errorText } from './api.ts';
  let {
    view,
    seq,
    action,
    participants,
    disabled,
    onsend
  }: {
    view: SessionView;
    seq: number;
    action: AllowedAction;
    participants: Participant[];
    disabled: boolean;
    onsend: (command: V2Command) => void;
  } = $props();
  const scope = untrack(() => captureScope(view));
  let amount = $state<number | undefined>(),
    text = $state(''),
    reason = $state(''),
    target = $state(''),
    error = $state('');
  const minimum = $derived(action.minimum);
  const chosenTarget = $derived(action.targets?.includes(target) ? target : action.targets?.[0]);
  const titles: Record<string, string> = {
    answer: 'Ваш ответ',
    wager: 'Ваша закрытая ставка',
    bid: 'Аукцион',
    select_price: 'Стоимость вопроса',
    select_recipient: 'Кому передать вопрос',
    eliminate_theme: 'Удалить тему финала',
    judge: 'Оценить ответ',
    reveal: 'Раскрыть',
    correct_judgement: 'Исправить решение',
    adjust_score: 'Изменить счёт',
    appeal: 'Подать апелляцию',
    resolve_appeal: 'Рассмотреть апелляцию',
    transfer_host: 'Передать роль ведущего',
    extend: 'Продлить время',
    restart_stage: 'Перезапустить этап'
  };
  function label(id: string) {
    if (['appeal', 'correct_judgement'].includes(action.type)) {
      const decision = view.decisions?.find((value) => value.id === id);
      return decision
        ? `${participants.find((p) => p.id === decision.participant_id)?.name || 'Игрок'}: ${decision.delta > 0 ? '+' : ''}${decision.delta}`
        : 'Решение ' + id.slice(0, 8);
    }
    if (action.type === 'resolve_appeal') {
      const appeal = view.appeals?.find((value) => value.id === id);
      return appeal
        ? `${participants.find((p) => p.id === appeal.participant_id)?.name || 'Игрок'}: ${appeal.reason}`
        : 'Апелляция';
    }
    if (action.type === 'eliminate_theme')
      return view.final?.themes.find((theme) => theme.id === id)?.name || 'Тема';
    return participants.find((p) => p.id === id)?.name || id;
  }
  function submit(data: Record<string, unknown>) {
    error = '';
    try {
      onsend(prepareAction(view, seq, action.type, data, scope));
    } catch (e) {
      error = errorText(e);
    }
  }
  function values() {
    const result: Record<string, unknown> = {};
    const type = action.type;
    if (['wager', 'select_price'].includes(type)) result.amount = amount ?? minimum;
    if (type === 'answer') result.text = text;
    if (['select_recipient', 'judge', 'adjust_score', 'transfer_host'].includes(type))
      result.target_id = chosenTarget;
    if (type === 'eliminate_theme') result.theme_id = chosenTarget;
    if (['correct_judgement', 'appeal'].includes(type)) result.judgement_id = chosenTarget;
    if (type === 'resolve_appeal') result.appeal_id = chosenTarget;
    if (
      ['correct_judgement', 'adjust_score', 'appeal', 'resolve_appeal', 'restart_stage'].includes(
        type
      )
    )
      result.reason = reason;
    if (type === 'adjust_score') result.delta = amount ?? 0;
    if (type === 'extend') result.seconds = amount ?? minimum ?? 15;
    return result;
  }
</script>

<form
  class="panel stack game-action"
  onsubmit={(event) => {
    event.preventDefault();
    submit(values());
  }}
>
  <h3>{titles[action.type] || action.type}</h3>
  {#if action.type === 'answer'}<label
      >Текст ответа<textarea
        bind:value={text}
        required
        maxlength="1024"
        placeholder="Ответ виден вам; ведущий получит его после закрытия сбора."></textarea><span
        class="helper"
        class:error-text={answerBytes(text) > 1024}
        >{answerBytes(text)} / 1024 байт в сообщении. Русская буква обычно занимает 2 байта; специальные
        символы — больше.</span
      ></label
    >{/if}
  {#if ['wager', 'select_price', 'bid', 'adjust_score', 'extend'].includes(action.type)}<label
      >{action.type === 'extend'
        ? 'Секунд'
        : action.type === 'adjust_score'
          ? 'Изменение очков'
          : 'Сумма'}<input
        type="number"
        value={amount ?? minimum ?? (action.type === 'extend' ? 15 : 0)}
        oninput={(event) => (amount = Number(event.currentTarget.value))}
        min={action.minimum}
        max={action.maximum}
        step="1"
        required={action.type !== 'bid'}
      /><span class="helper"
        >{action.minimum !== undefined ? `От ${action.minimum}` : ''}{action.maximum !== undefined
          ? ` до ${action.maximum}`
          : ''}{action.step ? ` · шаг ${action.step}` : ''}</span
      ></label
    >{/if}
  {#if action.targets?.length && action.type !== 'reveal'}<label
      >{action.type === 'eliminate_theme'
        ? 'Тема'
        : ['appeal', 'correct_judgement'].includes(action.type)
          ? 'Решение'
          : action.type === 'resolve_appeal'
            ? 'Апелляция'
            : 'Участник'}<select
        value={chosenTarget}
        onchange={(event) => (target = event.currentTarget.value)}
        >{#each action.targets as id}<option value={id}>{label(id)}</option>{/each}</select
      ></label
    >{/if}
  {#if ['correct_judgement', 'adjust_score', 'appeal', 'resolve_appeal', 'restart_stage'].includes(action.type)}<label
      >Причина<textarea bind:value={reason} required maxlength="1024"></textarea><span
        class="helper"
        class:error-text={utf8Length(reason) > 1024}>{utf8Length(reason)} / 1024 байт UTF-8</span
      ></label
    >{/if}
  {#if action.type === 'wager'}<p class="helper">
      Ставка фиксируется один раз. До раскрытия её видите только вы.
    </p>{/if}
  {#if action.type === 'restart_stage'}<p class="helper">
      Сбор на этом этапе начнётся заново. Введённые ставки и ответы текущего незавершённого этапа
      сбросятся.
    </p>{/if}
  <div class="row">
    {#if action.type === 'bid'}{#each action.modes || [] as mode}<button
          type="button"
          class:primary={mode === 'bid'}
          {disabled}
          onclick={() => submit({ mode, ...(mode === 'bid' ? { amount: amount ?? minimum } : {}) })}
          >{mode === 'bid' ? 'Сделать ставку' : mode === 'all_in' ? 'Ва-банк' : 'Пас'}</button
        >{/each}{:else if ['judge', 'correct_judgement'].includes(action.type)}<button
        type="button"
        class="primary"
        {disabled}
        onclick={() => submit({ ...values(), correct: true })}>✓ Верно</button
      ><button
        type="button"
        class="danger"
        {disabled}
        onclick={() => submit({ ...values(), correct: false })}>× Неверно</button
      >{:else if action.type === 'reveal'}{#if action.targets?.length}{#each action.targets as id}<button
            type="button"
            {disabled}
            onclick={() => submit({ target_id: id })}>Раскрыть: {label(id)}</button
          >{/each}{:else}<button {disabled}>Раскрыть ответ</button>{/if}{:else}<button
        class="primary"
        {disabled}
        >{action.type === 'answer'
          ? 'Отправить ответ'
          : action.type === 'wager'
            ? 'Зафиксировать ставку'
            : titles[action.type]}</button
      >{/if}
  </div>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
</form>
