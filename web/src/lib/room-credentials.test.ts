import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  saveRoomCredentials,
  readRoomCredentials,
  clearRoomCredentials
} from './room-credentials.ts';
test('new host survives denied storage in memory and revoked credentials cannot reappear', () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    get() {
      throw new Error('denied');
    }
  });
  try {
    const host = {
      room_id: 'room-denied',
      participant_id: 'host',
      token: 'secret',
      invite_token: 'invite'
    };
    saveRoomCredentials(host);
    assert.equal(readRoomCredentials(host.room_id)?.token, 'secret');
    clearRoomCredentials(host.room_id);
    assert.equal(readRoomCredentials(host.room_id), null);
  } finally {
    if (descriptor) Object.defineProperty(globalThis, 'sessionStorage', descriptor);
    else Reflect.deleteProperty(globalThis, 'sessionStorage');
  }
});
test('revocation shadows stale storage even when removeItem is denied', () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  const guest = { room_id: 'room-stale', participant_id: 'guest', token: 'old' };
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: {
      getItem: () => JSON.stringify(guest),
      setItem() {},
      removeItem() {
        throw new Error('denied');
      }
    }
  });
  try {
    assert.equal(readRoomCredentials(guest.room_id)?.token, 'old');
    clearRoomCredentials(guest.room_id);
    assert.equal(readRoomCredentials(guest.room_id), null);
    saveRoomCredentials({ ...guest, token: 'new' });
    assert.equal(readRoomCredentials(guest.room_id)?.token, 'new');
  } finally {
    if (descriptor) Object.defineProperty(globalThis, 'sessionStorage', descriptor);
    else Reflect.deleteProperty(globalThis, 'sessionStorage');
  }
});
test('conditional revocation cannot erase a newer credential for the same room', () => {
  const fresh = { room_id: 'same-room-generation', participant_id: 'new', token: 'new-token' };
  saveRoomCredentials(fresh);
  clearRoomCredentials(fresh.room_id, 'old-token');
  assert.equal(readRoomCredentials(fresh.room_id)?.token, 'new-token');
  clearRoomCredentials(fresh.room_id, 'new-token');
  assert.equal(readRoomCredentials(fresh.room_id), null);
});
