import { writable } from 'svelte/store';
import type { User } from './types';
import { api } from './api';
export const user = writable<User | null>(null);
// Invalidate even when a logout/login returns to the same account.
export const sessionEpoch = writable(0);
let epoch = 0;
let request = 0;
let channel: BroadcastChannel | undefined;
const authKey = 'si-auth-change';
function invalidate() {
  ++request;
  sessionEpoch.set(++epoch);
  user.set(null);
}
export function authenticationChanged() {
  invalidate();
  // No identity, credentials or personal data are sent between tabs.
  if (channel) channel.postMessage('changed');
  else {
    try {
      localStorage.setItem(authKey, crypto.randomUUID());
    } catch {}
  }
}
export function watchAuthentication() {
  const changed = () => {
    invalidate();
    void refreshUser();
  };
  try {
    channel = new BroadcastChannel(authKey);
    channel.onmessage = changed;
  } catch {}
  const storage = (event: StorageEvent) => {
    if (event.key === authKey) changed();
  };
  window.addEventListener('storage', storage);
  return () => {
    channel?.close();
    channel = undefined;
    window.removeEventListener('storage', storage);
    ++request;
  };
}
export async function refreshUser() {
  const current = ++request;
  try {
    const data = await api<{ user: User }>('/account/me');
    if (current !== request) return null;
    user.set(data.user);
    return data.user;
  } catch {
    if (current === request) user.set(null);
    return null;
  }
}
