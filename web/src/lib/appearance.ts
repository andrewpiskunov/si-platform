import type { PreparedMedia } from './types.ts';
export type BuiltinTheme = 'classic' | 'pirate' | 'anime';
export type Appearance =
  | { builtin: BuiltinTheme; theme_id?: never; revision?: never; hash?: never }
  | { theme_id: string; revision: number; hash: string; builtin?: never };
export interface ThemeManifest {
  schema_version: number;
  name: string;
  author: string;
  provenance: string;
  font: string;
  palette: Record<string, string>;
  cards: { radius: number; border_width: number };
  buttons: { radius: number; border_width: number };
  background?: string;
  logo?: string;
  ornament?: string;
}
export interface ThemeCard {
  id: string;
  owner_id: string;
  name: string;
  author: string;
  revision: number;
  size_bytes: number;
  blocked: boolean;
}
export interface PreparedTheme {
  manifest: ThemeManifest;
  assets: { name: string; prepared: PreparedMedia }[];
  hash: string;
  original_key: string;
  original_size: number;
  size_bytes: number;
}
export interface ThemeVersion {
  card: ThemeCard;
  prepared: PreparedTheme;
}
export interface ThemeAsset {
  index: number;
  name: string;
  width: number;
  height: number;
  standard_size: number;
  economy_size: number;
}
export interface RoomAppearance {
  appearance: Appearance;
  status: 'ready' | 'unavailable';
  manifest?: ThemeManifest;
  assets: ThemeAsset[];
}
export interface ThemePreferences {
  classic: boolean;
  economy: boolean;
}
export const fonts: Record<string, string> = {
  sans: "'Segoe UI',Arial,sans-serif",
  serif: "Georgia,'Times New Roman',serif",
  mono: "'Cascadia Code',Consolas,monospace"
};
const paletteVariables: Record<string, string> = {
  background: 'bg',
  surface: 'surface',
  surface_alt: 'surface-2',
  border: 'line',
  text: 'text',
  muted: 'muted',
  accent: 'accent',
  accent_text: 'accent-text',
  danger: 'danger',
  success: 'success',
  focus: 'focus'
};
export function manifestStyle(manifest: ThemeManifest): string {
  const values: string[] = [];
  if (manifest.schema_version !== 1 || !Object.hasOwn(fonts, manifest.font))
    throw new Error('invalid_theme');
  for (const [field, variable] of Object.entries(paletteVariables)) {
    const value = manifest.palette?.[field];
    if (typeof value !== 'string' || !/^#[0-9a-f]{6}$/i.test(value))
      throw new Error('invalid_theme');
    values.push(`--${variable}:${value}`);
  }
  for (const [name, shape] of [
    ['card', manifest.cards],
    ['button', manifest.buttons]
  ] as const) {
    if (
      !shape ||
      !Number.isInteger(shape.radius) ||
      shape.radius < 4 ||
      shape.radius > 24 ||
      !Number.isInteger(shape.border_width) ||
      shape.border_width < 1 ||
      shape.border_width > 3
    )
      throw new Error('invalid_theme');
    values.push(`--${name}-radius:${shape.radius}px`, `--${name}-border:${shape.border_width}px`);
  }
  values.push(`--theme-font:${fonts[manifest.font]}`);
  return values.join(';');
}
export function roomContext(url: {
  pathname: string;
  searchParams: { get(name: string): string | null };
}): string {
  const route = /^\/rooms\/([a-f0-9]{32})\/?$/.exec(url.pathname);
  const id = route?.[1] || url.searchParams.get('room') || '';
  return /^[a-f0-9]{32}$/.test(id) ? id : '';
}
export function contextLink(path: string, room: string): string {
  if (!room || !path.startsWith('/') || path.startsWith('//') || path.startsWith('/rooms/'))
    return path;
  const url = new URL(path, 'https://local.invalid');
  url.searchParams.set('room', room);
  return url.pathname + url.search + url.hash;
}
export function themePreferences(raw: string | null, mobile = false): ThemePreferences {
  try {
    const value = JSON.parse(raw || 'null');
    return {
      classic: value?.classic === true,
      economy: typeof value?.economy === 'boolean' ? value.economy : mobile
    };
  } catch {
    return { classic: false, economy: mobile };
  }
}
export function checkAssetBudget(
  assets: ThemeAsset[],
  quality: 'standard' | 'economy',
  manifest: ThemeManifest
): number {
  if (!Array.isArray(assets) || assets.length > 3) throw new Error('invalid_theme');
  const limit = quality === 'economy' ? 1024 * 1024 : 2 * 1024 * 1024;
  let total = 0;
  const names = new Set<string>(),
    indexes = new Set<number>();
  for (const asset of assets) {
    const size = quality === 'economy' ? asset.economy_size : asset.standard_size;
    if (
      !Number.isSafeInteger(size) ||
      size <= 0 ||
      !Number.isInteger(asset.index) ||
      asset.index < 0 ||
      names.has(asset.name) ||
      indexes.has(asset.index)
    )
      throw new Error('invalid_theme');
    names.add(asset.name);
    indexes.add(asset.index);
    total += size;
  }
  if (total > limit) throw new Error('theme_budget');
  for (const role of ['background', 'logo', 'ornament'] as const)
    if (manifest[role] && !names.has(manifest[role]!)) throw new Error('invalid_theme');
  return total;
}
export class AppearanceResources {
  private request: AbortController | null = null;
  private urls: string[] = [];
  private revoke: (url: string) => void;
  constructor(revoke: (url: string) => void = (url) => URL.revokeObjectURL(url)) {
    this.revoke = revoke;
  }
  begin() {
    this.request?.abort();
    this.request = new AbortController();
    return this.request;
  }
  current(request: AbortController) {
    return this.request === request && !request.signal.aborted;
  }
  commit(request: AbortController, urls: string[]) {
    if (!this.current(request)) {
      urls.forEach(this.revoke);
      return false;
    }
    this.urls.forEach(this.revoke);
    this.urls = urls;
    return true;
  }
  clear() {
    this.request?.abort();
    this.request = null;
    this.urls.forEach(this.revoke);
    this.urls = [];
  }
}
export function appearanceKey(appearance?: Appearance): string {
  return (
    appearance?.builtin ||
    (appearance ? `${appearance.theme_id}:${appearance.revision}:${appearance.hash}` : '')
  );
}
export async function fetchThemeAssets(
  assets: ThemeAsset[],
  quality: 'standard' | 'economy',
  manifest: ThemeManifest,
  path: (index: number) => string,
  signal: AbortSignal,
  token?: string
) {
  checkAssetBudget(assets, quality, manifest);
  const urls: Record<string, string> = {};
  try {
    for (const asset of assets) {
      const response = await fetch(path(asset.index), {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        credentials: 'same-origin',
        signal
      });
      if (!response.ok) throw new Error('theme_unavailable');
      const size = quality === 'economy' ? asset.economy_size : asset.standard_size;
      const blob = await readThemeImage(response, size);
      const bitmap = await createImageBitmap(blob);
      const valid =
        bitmap.width > 0 &&
        bitmap.height > 0 &&
        bitmap.width <= asset.width &&
        bitmap.height <= asset.height;
      bitmap.close();
      if (!valid || signal.aborted) throw new Error('invalid_theme');
      urls[asset.name] = URL.createObjectURL(blob);
    }
    return urls;
  } catch (error) {
    Object.values(urls).forEach((url) => URL.revokeObjectURL(url));
    throw error;
  }
}

export async function readThemeImage(response: Response, size: number): Promise<Blob> {
  if (response.headers.get('Content-Type')?.split(';')[0] !== 'image/png' || !response.body)
    throw new Error('invalid_theme');
  const reader = response.body.getReader(),
    chunks: Uint8Array<ArrayBuffer>[] = [];
  let received = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      received += value.byteLength;
      if (received > size) throw new Error('theme_budget');
      chunks.push(new Uint8Array(value));
    }
    if (received !== size) throw new Error('invalid_theme');
    return new Blob(chunks, { type: 'image/png' });
  } catch (error) {
    await reader.cancel();
    throw error;
  } finally {
    reader.releaseLock();
  }
}
