import { clearRoomCredentials, readRoomCredentials } from './room-credentials.ts';
import { writable } from 'svelte/store';
import { api, APIError } from './api.ts';
import {
  appearanceKey,
  AppearanceResources,
  manifestStyle,
  fetchThemeAssets,
  type Appearance,
  type BuiltinTheme,
  type RoomAppearance,
  type ThemePreferences,
  type ThemeManifest
} from './appearance.ts';
export type RoomThemeName = BuiltinTheme;
export const roomThemeContext = Symbol('room-theme');
export interface ThemeDetail {
  room: string;
  builtin: BuiltinTheme | null;
  style: string;
  images: Record<string, string>;
  manifest?: ThemeManifest;
  preferences: ThemePreferences;
  notice: string;
  appearance?: Appearance;
}
export function createRoomTheme() {
  const state = writable<RoomThemeName | null>(null);
  let value: ThemeDetail = {
    room: '',
    builtin: null,
    style: '',
    images: {},
    preferences: { classic: false, economy: false },
    notice: ''
  };
  const detail = writable(value),
    resources = new AppearanceResources();
  let token = '',
    loadedKey = '',
    observedKey = '',
    epoch = -1,
    online = false;
  function publish() {
    detail.set(value);
    state.set(value.builtin);
  }
  function classic(notice = '') {
    loadedKey = '';
    resources.clear();
    value = {
      ...value,
      builtin: value.room ? 'classic' : null,
      style: '',
      images: {},
      manifest: undefined,
      notice
    };
    publish();
  }
  async function refresh() {
    if (!value.room || !token) return;
    const room = value.room,
      credential = token,
      request = resources.begin();
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(5000)]);
    try {
      const response = await api<RoomAppearance>(
        `/rooms/${room}/appearance`,
        'GET',
        undefined,
        credential,
        signal
      );
      if (!resources.current(request)) return;
      value = { ...value, appearance: response.appearance };
      if (response.status !== 'ready') throw new Error('theme_unavailable');
      if (value.preferences.classic || response.appearance.builtin) {
        resources.commit(request, []);
        loadedKey = '';
        value = {
          ...value,
          builtin: value.preferences.classic ? 'classic' : response.appearance.builtin || 'classic',
          style: '',
          images: {},
          manifest: undefined,
          notice: ''
        };
        publish();
        return;
      }
      if (!response.manifest) throw new Error('invalid_theme');
      const style = manifestStyle(response.manifest),
        quality = value.preferences.economy ? 'economy' : 'standard';
      const key = appearanceKey(response.appearance) + ':' + quality;
      if (loadedKey === key) {
        value = { ...value, notice: '' };
        publish();
        return;
      }
      const images = await fetchThemeAssets(
        response.assets || [],
        quality,
        response.manifest,
        (index) =>
          `/api/rooms/${room}/appearance/assets/${index}?hash=${encodeURIComponent(response.appearance.hash || '')}&quality=${quality}`,
        signal,
        credential
      );
      if (!resources.commit(request, Object.values(images))) return;
      loadedKey = key;
      value = { ...value, builtin: null, style, images, manifest: response.manifest, notice: '' };
      publish();
    } catch {
      if (resources.current(request))
        classic('Оформление недоступно. Включена классическая тема; игра продолжается.');
    }
  }
  return {
    subscribe: state.subscribe,
    detail: { subscribe: detail.subscribe },
    select(theme: string) {
      value = { ...value, builtin: theme === 'pirate' || theme === 'anime' ? theme : 'classic' };
      publish();
    },
    enter(room: string, credential: string, preferences: ThemePreferences) {
      if (room === value.room && credential === token) return;
      resources.clear();
      token = credential;
      loadedKey = '';
      observedKey = '';
      epoch = -1;
      online = false;
      value = {
        room,
        builtin: room ? 'classic' : null,
        style: '',
        images: {},
        preferences,
        notice: ''
      };
      publish();
    },
    observe(appearance: Appearance | undefined, nextEpoch: number, ready: boolean) {
      const key = appearanceKey(appearance);
      const changed = (key && key !== observedKey) || nextEpoch !== epoch || (ready && !online);
      observedKey = key;
      epoch = nextEpoch;
      online = ready;
      if (ready && changed) void refresh();
    },
    preferences(preferences: ThemePreferences) {
      value = { ...value, preferences };
      publish();
      if (preferences.classic) classic();
      void refresh();
    },
    refresh,
    reset() {
      resources.clear();
      token = '';
      loadedKey = '';
      value = {
        room: '',
        builtin: null,
        style: '',
        images: {},
        preferences: { classic: false, economy: false },
        notice: ''
      };
      publish();
    }
  };
}
export type RoomTheme = ReturnType<typeof createRoomTheme>;
// This channel never joins game presence and carries no game state or commands.
export class AppearanceConnection {
  private socket: WebSocket | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private stopped = false;
  private attempts = 0;
  private room: string;
  private token: string;
  private refresh: () => void;
  private revoked: () => void;
  constructor(room: string, token: string, refresh: () => void, revoked: () => void = () => {}) {
    this.room = room;
    this.token = token;
    this.refresh = refresh;
    this.revoked = revoked;
  }
  private connecting = false;
  async connect() {
    if (this.stopped || this.connecting) return;
    this.connecting = true;
    try {
      await api(
        `/rooms/${encodeURIComponent(this.room)}/session`,
        'POST',
        {},
        this.token,
        AbortSignal.timeout(10000)
      );
    } catch (error) {
      if (this.stopped) return;
      if (error instanceof APIError && error.status === 401) {
        this.revoke();
        return;
      }
      if (!(error instanceof APIError && error.code === 'guest_session_mismatch')) return;
    } finally {
      this.connecting = false;
    }
    if (this.stopped) return;
    const url = new URL(`/api/rooms/${this.room}/ws?appearance_only=1`, location.origin);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(url);
    this.socket = socket;
    socket.onopen = () => {
      if (this.stopped) {
        socket.close();
        return;
      }
      socket.send(JSON.stringify({ v: 1, type: 'authenticate', token: this.token }));
    };
    socket.onmessage = (event) => {
      try {
        if (this.stopped || socket !== this.socket) return;
        const message = JSON.parse(event.data);
        if (message.type === 'appearance_refresh') {
          this.attempts = 0;
          this.refresh();
        }
        if (message.code === 'unauthorized') {
          this.revoke();
        }
      } catch {}
    };
    socket.onclose = () => {
      if (!this.stopped && socket === this.socket)
        this.timer = setTimeout(() => this.connect(), Math.min(1000 * 2 ** this.attempts++, 15000));
    };
    socket.onerror = () => socket.close();
  }
  private revoke() {
    if (this.stopped) return;
    const saved = readRoomCredentials(this.room);
    const superseded = !!saved && saved.token !== this.token;
    clearRoomCredentials(this.room, this.token);
    this.token = '';
    this.close();
    if (!superseded) this.revoked();
  }
  close() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.socket?.close();
  }
}
