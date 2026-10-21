import type { RoomSettings } from './types.ts';

type StorageAccess = () => Pick<Storage, 'getItem' | 'setItem'>;
export class RulesVersionObserver {
  private version = 0;
  private key: string;
  private storage: StorageAccess;
  constructor(identity: string, storage: StorageAccess = () => sessionStorage) {
    this.key = `rules-version:${identity}`;
    this.storage = storage;
    try {
      const saved = Number(storage().getItem(this.key));
      if (Number.isSafeInteger(saved) && saved > 0) this.version = saved;
    } catch {}
  }
  observe(version: number): boolean {
    if (!Number.isSafeInteger(version) || version <= this.version || version < 1) return false;
    const changed = this.version > 0;
    this.version = version;
    try {
      this.storage().setItem(this.key, String(version));
    } catch {}
    return changed;
  }
}

const copy = <T>(value: T): T => JSON.parse(JSON.stringify(value));
const equal = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
export interface FormState<T> {
  value: T;
  latest: T;
  dirty: boolean;
  conflict: boolean;
  submitted?: T;
}
export function createForm<T>(value: T): FormState<T> {
  return { value: copy(value), latest: copy(value), dirty: false, conflict: false };
}
export function editForm<T>(form: FormState<T>, value: T): FormState<T> {
  const dirty = !equal(value, form.latest);
  return { ...form, value: copy(value), dirty, conflict: dirty && form.conflict };
}
export function observeForm<T>(form: FormState<T>, latest: T): FormState<T> {
  if (equal(latest, form.latest)) return form;
  if (!form.dirty || equal(latest, form.value)) return createForm(latest);
  const own = form.submitted !== undefined && equal(latest, form.submitted);
  return {
    ...form,
    latest: copy(latest),
    conflict: !own,
    submitted: own ? undefined : form.submitted
  };
}
export function resetForm<T>(form: FormState<T>): FormState<T> {
  return createForm(form.latest);
}
export function submitForm<T>(form: FormState<T>): FormState<T> {
  return { ...form, submitted: copy(form.value), conflict: false };
}

export function normalizedSettings(settings?: RoomSettings | null): RoomSettings {
  return {
    visibility: settings?.visibility === 'public' ? 'public' : 'private',
    voice_url: settings?.voice_url || ''
  };
}
export function voiceDestination(value: string): { href: string; hostname: string } | null {
  if (!value || new TextEncoder().encode(value).length > 2048 || /[\s\\\p{Cc}]/u.test(value))
    return null;
  const authority = /^https:\/\/([^/?#]*)/i.exec(value)?.[1];
  if (!authority || authority.includes('@')) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' || !url.hostname || url.username || url.password) return null;
    return { href: url.href, hostname: url.hostname };
  } catch {
    return null;
  }
}
export function validRoomSettings(value: unknown): value is RoomSettings {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const settings = value as Record<string, unknown>;
  return (
    Object.keys(settings).every((key) => ['visibility', 'voice_url'].includes(key)) &&
    ['private', 'public'].includes(String(settings.visibility)) &&
    typeof settings.voice_url === 'string' &&
    (settings.voice_url === '' || voiceDestination(settings.voice_url) !== null)
  );
}
export function roomListQuery(params: URLSearchParams): string {
  const query = new URLSearchParams();
  const after = params.get('after'),
    limit = params.get('limit') ?? '50';
  if (after !== null && !/^[0-9a-f]{32}$/.test(after)) throw new Error('invalid');
  if (!/^\d+$/.test(limit) || Number(limit) < 1 || Number(limit) > 50) throw new Error('invalid');
  if (after) query.set('after', after);
  query.set('limit', String(Number(limit)));
  return query.toString();
}
