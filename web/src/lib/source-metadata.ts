export function sourceDate(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return '';
  const date = new Date(`${value}T12:00:00Z`);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString('ru-RU', {timeZone:'UTC',day:'numeric',month:'long',year:'numeric'});
}
export function safeSourceLink(value: string): boolean {
  try { const u = new URL(value); return ['https:', 'http:'].includes(u.protocol) && !!u.hostname && !u.username && !u.password; } catch { return false; }
}
export function sourcePercent(value: number | null | undefined): string { return value == null ? '—' : `${value}%`; }
