import type { Credentials } from './types.ts';
// A null entry shadows stale storage after revocation, including denied removeItem.
const memory = new Map<string, Credentials | null>();
export function saveRoomCredentials(credentials: Credentials): void {
  const saved = {
    room_id: credentials.room_id,
    participant_id: credentials.participant_id,
    token: credentials.token
  };
  memory.set(saved.room_id, saved);
  try {
    sessionStorage.setItem(`room:${saved.room_id}`, JSON.stringify(saved));
  } catch {}
}
export function readRoomCredentials(room: string): Credentials | null {
  if (memory.has(room)) return memory.get(room) || null;
  try {
    const saved = JSON.parse(sessionStorage.getItem(`room:${room}`) || 'null');
    if (
      saved?.room_id === room &&
      typeof saved.token === 'string' &&
      saved.token &&
      typeof saved.participant_id === 'string'
    ) {
      memory.set(room, saved);
      return saved;
    }
  } catch {}
  return null;
}
export function clearRoomCredentials(room: string): void {
  memory.set(room, null);
  try {
    sessionStorage.removeItem(`room:${room}`);
  } catch {}
}
