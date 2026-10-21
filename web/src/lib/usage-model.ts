import type { Usage } from './types.ts';

export const usageLimits = {
  storage: 10 * 1024 ** 3,
  packs: 200,
  themes: 100,
  uploads: 2,
  pack_revisions: 500,
  theme_revisions: 100,
  siq_bytes: 250 * 1024 ** 2,
  media_bytes: 100 * 1024 ** 2,
  theme_bytes: 5 * 1024 ** 2
};

export function formatUsageBytes(value: number | undefined): string {
  if (value === undefined) return 'Неизвестно';
  const units = ['Б', 'КиБ', 'МиБ', 'ГиБ'];
  const index = value > 0 ? Math.min(3, Math.floor(Math.log(value) / Math.log(1024))) : 0;
  return `${new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 2 }).format(value / 1024 ** index)} ${units[index]}`;
}

export function usageFailure(snapshot: Usage | null, status?: number) {
  return { snapshot: status === 401 ? null : snapshot, stale: status !== 401 && snapshot !== null };
}

type UsageRequest = { account: string | null; signal: AbortSignal; generation: number };
export class UsageRequestScope {
  private generation = 0;
  private controller?: AbortController;
  private disposed = false;

  begin(account: string | null): UsageRequest {
    this.controller?.abort();
    this.controller = new AbortController();
    return { account, signal: this.controller.signal, generation: ++this.generation };
  }

  current(request: UsageRequest, account: () => string | null): boolean {
    // Check lifecycle before evaluating a Svelte getter from a destroyed component.
    return (
      !this.disposed &&
      !request.signal.aborted &&
      request.generation === this.generation &&
      request.account === account()
    );
  }

  dispose() {
    this.disposed = true;
    this.controller?.abort();
    this.generation++;
  }
}
