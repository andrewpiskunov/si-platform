export function reasonError(reason: string, required = true): string {
  if (required && !reason.trim()) return 'Укажите причину решения.';
  if ([...reason].length > 2000 || new TextEncoder().encode(reason).length > 8000)
    return 'Причина должна содержать не более 2000 символов.';
  if (/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/u.test(reason))
    return 'Уберите управляющие символы из причины.';
  return '';
}
export function canResolve(status: string, busy: boolean): boolean {
  return status === 'open' && !busy;
}
export function mergePage<T extends { id: string | number }>(
  previous: T[],
  incoming: T[] | null,
  append: boolean
): T[] {
  return [
    ...new Map(
      [...(append ? previous : []), ...(incoming || [])].map((item) => [item.id, item])
    ).values()
  ];
}
export function previewKey(packId: string, revision: number, questionId: string): string {
  return JSON.stringify([packId, revision, questionId]);
}
// Callbacks are evaluated only while mounted and current: even reading a Svelte
// derived getter after destruction can throw before an ordinary boolean guard.
export class RequestGate {
  private generation = 0;
  private alive = true;
  start(): number {
    return ++this.generation;
  }
  apply(token: number, update: () => void): void {
    if (this.alive && token === this.generation) update();
  }
  dispose(): void {
    this.alive = false;
    ++this.generation;
  }
}
export const decisionLabels: Record<string, string> = {
  approve: 'Опубликован',
  reject: 'Возвращён автору',
  block: 'Заблокирован'
};
export const resolutionLabels: Record<string, string> = {
  decision: 'Решение по пакету',
  resolved: 'Рассмотрение завершено',
  dismissed: 'Жалоба отклонена',
  legacy: 'Рассмотрена ранее'
};
