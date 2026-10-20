export class APIError extends Error {
  code: string;
  status: number;
  constructor(code: string, status: number) {
    super(code);
    this.code = code;
    this.status = status;
  }
}
export async function api<T>(
  path: string,
  method = 'GET',
  body?: unknown,
  token?: string
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await fetch(`/api${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin'
  });
  const data = await response.json().catch(() => ({ error: 'unavailable' }));
  if (!response.ok) throw new APIError(data.error || 'unavailable', response.status);
  return data as T;
}
const errors: Record<string, string> = {
  unauthorized: 'Войдите в аккаунт, чтобы продолжить.',
  forbidden: 'У вас нет доступа к этому действию.',
  unavailable: 'Сервис временно недоступен. Попробуйте ещё раз.',
  not_found: 'Не удалось найти запрошенный материал.',
  invalid_input: 'Проверьте введённые данные.',
  invalid: 'Проверьте поля и ошибки пакета.',
  conflict: 'Пакет уже изменён в другой вкладке. Ваши изменения сохранены в редакторе.',
  username_taken: 'Этот логин уже занят.',
  rate_limited: 'Слишком много попыток. Подождите немного.',
  quota: 'Достигнут лимит хранения.',
  blocked: 'Пакет заблокирован.',
  room_full: 'В комнате больше нет свободных мест.',
  question_in_progress: 'Дождитесь конца вопроса или войдите зрителем.',
  not_enough_players: 'Для начала нужны хотя бы два игрока.',
  disconnected: 'Соединение восстанавливается. Подождите.',
  invalid_phase: 'Состояние игры изменилось. Попробуйте снова.',
  stale_question: 'Этот вопрос уже завершён.',
  stale_state: 'Табло изменилось. Выберите вопрос снова.',
  command_conflict: 'Это действие уже отправлено с другими данными.'
};
export function errorText(error: unknown): string {
  const code = error instanceof Error ? error.message : String(error);
  return errors[code] || 'Не удалось выполнить действие. Попробуйте ещё раз.';
}
export const statusText: Record<string, string> = {
  draft: 'Черновик',
  pending: 'На проверке',
  published: 'Опубликован',
  rejected: 'Нужны изменения',
  withdrawn: 'Снят с публикации',
  blocked: 'Заблокирован'
};
export const bytes = (value: number) =>
  value >= 1024 * 1024 ? `${(value / 1024 / 1024).toFixed(1)} МБ` : `${Math.ceil(value / 1024)} КБ`;
