import { writable } from 'svelte/store';
export type RoomThemeName = 'classic' | 'pirate' | 'anime';
export const roomThemeContext = Symbol('room-theme');

// The layout creates a store for its own component tree. No mutable theme state
// is shared between SSR requests or independently mounted applications.
export function createRoomTheme() {
  const state = writable<RoomThemeName | null>(null);
  return {
    subscribe: state.subscribe,
    select(theme: string) {
      state.set(theme === 'pirate' || theme === 'anime' ? theme : 'classic');
    },
    reset() {
      state.set(null);
    }
  };
}
export type RoomTheme = ReturnType<typeof createRoomTheme>;
