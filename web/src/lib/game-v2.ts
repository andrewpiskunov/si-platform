export interface Rules {
  preset: 'classic' | 'friends' | 'custom';
  selection_seconds: number;
  answer_seconds: number;
  special_seconds: number;
  wager_seconds: number;
  final_seconds: number;
  penalties: boolean;
  buzzer_mode: 'first' | 'random';
  window_ms: number;
  false_start: boolean;
  false_start_seconds: number;
  no_risk_multiplier: 2;
}
export interface Stage {
  id: string;
  kind: string;
  instance_id?: string;
  deadline_ms?: number;
  remaining_ms?: number;
  expired: boolean;
  turn_id?: string;
}
export interface AllowedAction {
  type: string;
  minimum?: number;
  maximum?: number;
  step?: number;
  modes?: string[] | null;
  targets?: string[] | null;
  requires_seq?: boolean;
}
export interface Decision {
  id: string;
  participant_id: string;
  correct: boolean;
  delta: number;
  reason: string;
  question_id: string;
  instance_id?: string;
  supersedes?: string;
}
export interface Appeal {
  id: string;
  judgement_id: string;
  participant_id: string;
  reason: string;
  resolved: boolean;
  resolution?: string;
}
export interface MediaView {
  id: string;
  kind: string;
  duration_ms: number;
  playback_end_position_ms: number;
  stopped_position_ms?: number;
  width: number;
  height: number;
  standard: { mime: string; size: number };
  economy: { mime: string; size: number };
  start_at_ms: number;
  paused_position_ms: number;
}
export type MediaStatus = 'loading' | 'ready' | 'error' | 'hidden';
export interface SessionView {
  readiness?: { id: string; status: MediaStatus }[];
  version: 2;
  rules_version: number;
  rules: Rules;
  chooser_id: string;
  server_time_ms: number;
  stage: Stage;
  round?: { id: string; name: string; type: string };
  question?: {
    media?: MediaView;
    id: string;
    instance_id: string;
    type: string;
    theme: string;
    price: number;
    text: string;
    answer?: string;
    content_index: number;
    content_total: number;
    answer_index?: number;
    answer_total?: number;
  };
  players:
    | {
        id: string;
        wager_submitted: boolean;
        answer_submitted: boolean;
        judged: boolean;
        blocked_until_ms?: number;
        attempted: boolean;
      }[]
    | null;
  self: { wager?: number; answer?: string };
  submissions:
    { id: string; answer?: string; wager?: number; revealed: boolean; correct?: boolean }[] | null;
  auction?: { leader_id: string; amount: number; all_in: boolean; active_ids: string[] };
  final?: { themes: { id: string; name: string }[]; eligible_ids: string[]; order: string[] };
  decisions: Decision[] | null;
  appeals: Appeal[] | null;
  allowed_actions: AllowedAction[] | null;
}
export interface V2Command {
  v: 2;
  id: string;
  type: string;
  expected_seq?: number;
  data: Record<string, unknown>;
}
export interface ActionScope {
  stage_id: string;
  stage_kind: string;
  instance_id: string;
}
export function captureScope(view: SessionView): ActionScope {
  return {
    stage_id: view.stage.id,
    stage_kind: view.stage.kind,
    instance_id: view.stage.instance_id || ''
  };
}
export function actionFor(view: SessionView, type: string): AllowedAction | undefined {
  return view.allowed_actions?.find((action) => action.type === type);
}
const fields: Record<string, string[]> = {
  media_ready: ['status'],
  continue_media: [],
  wait_media: [],
  skip_question: ['reason'],
  set_rules: ['rules'],
  pause: [],
  resume: [],
  finish: [],
  extend: ['seconds'],
  restart_stage: ['reason'],
  advance: [],
  open_buzzer: [],
  buzz: [],
  answer: ['text'],
  wager: ['amount'],
  select_price: ['amount'],
  bid: ['mode', 'amount'],
  select_recipient: ['target_id'],
  eliminate_theme: ['theme_id'],
  reveal: ['target_id'],
  judge: ['target_id', 'correct'],
  correct_judgement: ['judgement_id', 'correct', 'reason'],
  adjust_score: ['target_id', 'delta', 'reason'],
  appeal: ['judgement_id', 'reason'],
  resolve_appeal: ['appeal_id', 'reason'],
  transfer_host: ['target_id']
};
const staged = new Set([
  'extend',
  'restart_stage',
  'media_ready',
  'continue_media',
  'wait_media',
  'skip_question',
  'advance',
  'open_buzzer',
  'buzz',
  'answer',
  'wager',
  'select_price',
  'bid',
  'select_recipient',
  'eliminate_theme',
  'reveal',
  'judge'
]);
const instanced = new Set([
  'media_ready',
  'continue_media',
  'wait_media',
  'skip_question',
  'advance',
  'open_buzzer',
  'buzz',
  'answer',
  'wager',
  'select_price',
  'bid',
  'select_recipient',
  'reveal',
  'judge'
]);
export function prepareAction(
  view: SessionView,
  seq: number,
  type: string,
  input: Record<string, unknown> = {},
  scope = captureScope(view),
  id: string = crypto.randomUUID()
): V2Command {
  const action = actionFor(view, type);
  if (!action || !fields[type]) throw new Error('action_unavailable');
  if (
    scope.stage_id !== view.stage.id ||
    scope.stage_kind !== view.stage.kind ||
    scope.instance_id !== (view.stage.instance_id || '')
  )
    throw new Error('stale_stage');
  if (Object.keys(input).some((key) => !fields[type].includes(key)))
    throw new Error('invalid_action_data');
  const data: Record<string, unknown> = {};
  if (staged.has(type)) data.stage_id = scope.stage_id;
  if (instanced.has(type)) {
    if (!scope.instance_id) throw new Error('stale_stage');
    data.instance_id = scope.instance_id;
  }
  for (const key of fields[type]) if (input[key] !== undefined) data[key] = input[key];
  const targetField = ['target_id', 'theme_id', 'judgement_id', 'appeal_id'].find((key) =>
    fields[type].includes(key)
  );
  if (targetField && !(type === 'reveal' && input[targetField] === undefined)) {
    if (
      typeof input[targetField] !== 'string' ||
      !input[targetField] ||
      !action.targets?.includes(input[targetField] as string)
    )
      throw new Error('invalid_target');
  }
  if (
    type === 'bid' &&
    (!action.modes?.includes(String(input.mode)) ||
      (input.mode !== 'bid' && input.amount !== undefined))
  )
    throw new Error('invalid_mode');
  const amountField =
    ['wager', 'select_price'].includes(type) || (type === 'bid' && input.mode === 'bid')
      ? 'amount'
      : type === 'extend'
        ? 'seconds'
        : type === 'adjust_score'
          ? 'delta'
          : undefined;
  if (amountField) {
    const value = input[amountField];
    if (
      typeof value !== 'number' ||
      !Number.isSafeInteger(value) ||
      (action.minimum !== undefined && value < action.minimum) ||
      (action.maximum !== undefined && value > action.maximum) ||
      (action.step !== undefined &&
        action.step > 0 &&
        value !== action.maximum &&
        value !== action.minimum &&
        (value - (type === 'bid' ? 0 : (action.minimum ?? 0))) % action.step !== 0)
    )
      throw new Error('invalid_amount');
  }
  if (fields[type].includes('correct') && typeof input.correct !== 'boolean')
    throw new Error('invalid_action_data');
  for (const key of ['text', 'reason'])
    if (
      fields[type].includes(key) &&
      (typeof input[key] !== 'string' || !(input[key] as string).trim())
    )
      throw new Error('invalid_action_data');
  if (type === 'answer' && answerBytes(input.text as string) > 1024)
    throw new Error('answer_too_long');
  if (typeof input.reason === 'string' && utf8Length(input.reason) > 1024)
    throw new Error('reason_too_long');
  if (
    type === 'media_ready' &&
    !['loading', 'ready', 'error', 'hidden'].includes(String(input.status))
  )
    throw new Error('invalid_action_data');
  if (type === 'set_rules' && (!input.rules || typeof input.rules !== 'object'))
    throw new Error('invalid_action_data');
  return { v: 2, id, type, ...(action.requires_seq ? { expected_seq: seq } : {}), data };
}
export function timerSeconds(
  stage: Stage,
  serverTime: number,
  elapsed = 0,
  paused = false
): number | null {
  if (paused) return Math.max(0, Math.ceil((stage.remaining_ms ?? 0) / 1000));
  if (!stage.deadline_ms) return stage.expired ? 0 : null;
  return Math.max(0, Math.ceil((stage.deadline_ms - serverTime - Math.max(0, elapsed)) / 1000));
}
export const stageNames: Record<string, string> = {
  prepare_content: 'Подготовка медиа',
  scheduled_content: 'Общий старт',
  select_question: 'Выбор вопроса',
  select_recipient: 'Кому достанется вопрос?',
  select_price: 'Выбор стоимости',
  auction: 'Аукцион',
  collect_wagers: 'Закрытые ставки',
  present_question: 'Читаем вопрос',
  present_answer: 'Пояснение к ответу',
  buzz_open: 'Кнопка открыта',
  buzz_window: 'Принимаем нажатия',
  collect_answer: 'Время ответа',
  collect_answers: 'Принимаем ответы',
  judge: 'Решение ведущего',
  reveal: 'Раскрытие ответов',
  resolved: 'Вопрос завершён',
  eliminate_theme: 'Убираем темы финала',
  select_final: 'Финальный вопрос'
};
export const questionTypes: Record<string, string> = {
  simple: 'Обычный',
  withButton: 'На кнопку',
  stake: 'Ставка',
  stakeAll: 'Общая ставка',
  secret: 'Секрет',
  forAll: 'Всем',
  noRisk: 'Без риска'
};
export function classicRules(): Rules {
  return {
    preset: 'classic',
    selection_seconds: 30,
    answer_seconds: 10,
    special_seconds: 30,
    wager_seconds: 30,
    final_seconds: 60,
    penalties: true,
    buzzer_mode: 'first',
    window_ms: 200,
    false_start: true,
    false_start_seconds: 3,
    no_risk_multiplier: 2
  };
}

export function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

// Match the server JSON encoder's escaped string budget, excluding the quotes.
export function answerBytes(value: string): number {
  return (
    utf8Length(
      JSON.stringify(value).replace(
        /[<>&\u2028\u2029]/g,
        (char) => '\\u' + char.charCodeAt(0).toString(16).padStart(4, '0')
      )
    ) - 2
  );
}
