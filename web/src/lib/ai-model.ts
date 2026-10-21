import type { PackDocument } from './types.ts';
export type AIRequest = {
  mode: 'theme' | 'pack';
  topic: string;
  audience: string;
  difficulty: number;
  themes: number;
  questions_per_theme: number;
};
export type AIResult = {
  title: string;
  themes: {
    name: string;
    questions: { text: string; answer: string; comment: string; sources: string[] }[];
  }[];
};
export type AIStatus = {
  enabled: boolean;
  provider: string;
  model: string;
  monthly_limit: number;
  spent: number;
  reserved: number;
  remaining: number;
};
export type AIQuote = {
  id: string;
  request: AIRequest;
  model: string;
  maximum_cost: number;
  expires_at: string;
  input_tokens: number;
  max_output_tokens: number;
};
export type AIJob = {
  id: string;
  status: string;
  request: AIRequest;
  result?: AIResult;
  model: string;
  maximum_cost: number;
  actual_cost?: number;
  cancel_requested: boolean;
  error?: string;
};
export type AIPage = { jobs: AIJob[] | null; next?: string };
export const money = (value: number | null | undefined) =>
  value == null ? 'неизвестно' : `$${(value / 1e6).toFixed(6)} USD`;
export const activeJob = (status: string) => ['queued', 'running'].includes(status);
export const jobLabel: Record<string, string> = {
  queued: 'В очереди',
  running: 'Выполняется',
  ready: 'Готово к проверке',
  failed: 'Не удалось получить результат',
  cancelled: 'Отменено',
  uncertain: 'Исход неизвестен — резерв удерживается'
};
export function aiError(error: unknown) {
  const code = error instanceof Error ? error.message : '';
  return (
    (
      {
        ai_disabled: 'Генерация отключена оператором. История доступна.',
        invalid_input: 'Проверьте поля, число вопросов и повторяющиеся тексты.',
        conflict:
          'Котировка или задание изменились, либо результат уже применён с другими правками. Обновите историю.',
        limit_exceeded: 'Достигнут лимит бюджета, очереди или частоты запросов. Повторите позже.',
        not_found: 'Задание больше недоступно.',
        unauthorized: 'Войдите снова, чтобы открыть задания.',
        forbidden: 'Нет доступа к этому действию.',
        duplicate_theme: 'Эта тема уже добавлена в пакет.',
        invalid_round: 'Выберите существующий обычный раунд.'
      } as Record<string, string>
    )[code] || 'Сервис временно недоступен. Повтор запуска использует тот же ключ задания.'
  );
}
export class AIQuoteState {
  private quote = '';
  private signature = '';
  private expires = 0;
  private receipt: { quote_id: string; idempotency_key: string } | null = null;
  set(id: string, signature: string, expires: number) {
    this.quote = id;
    this.signature = signature;
    this.expires = expires;
    this.receipt = null;
  }
  clear() {
    this.quote = '';
    this.receipt = null;
  }
  canStart(signature: string, now: number) {
    return (
      !!this.quote && signature === this.signature && (this.receipt !== null || now < this.expires)
    );
  }
  start(signature: string, now: number, key: () => string) {
    if (!this.canStart(signature, now)) throw new Error('conflict');
    return (this.receipt ??= { quote_id: this.quote, idempotency_key: key() });
  }
}
export function appendAITheme(current: PackDocument, preview: PackDocument, roundID: string) {
  const round = current.rounds.find(
    (r) => r.id === roundID && (r.type || 'standard') === 'standard'
  );
  if (!round) throw new Error('invalid_round');
  const themes = preview.rounds.flatMap((r) => r.themes);
  if (themes.length !== 1) throw new Error('invalid_input');
  const theme = themes[0];
  if (current.rounds.some((r) => r.themes.some((t) => t.id === theme.id)))
    throw new Error('duplicate_theme');
  round.themes.push(JSON.parse(JSON.stringify(theme)));
}

export function normalizeAIRequest(request: AIRequest): AIRequest {
  return { ...request, topic: request.topic.trim(), audience: request.audience.trim() };
}
