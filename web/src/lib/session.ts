import { writable } from 'svelte/store';
import type { User } from './types';
import { api } from './api';
export const user = writable<User | null>(null);
export async function refreshUser() {
  try {
    const data = await api<{ user: User }>('/account/me');
    user.set(data.user);
    return data.user;
  } catch {
    user.set(null);
    return null;
  }
}
