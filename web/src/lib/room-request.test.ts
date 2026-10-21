import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RoomRequestScope } from './room-request.ts';
test('late room responses cannot act after rejoin, replacement request or component destruction', () => {
  const scope = new RoomRequestScope();
  const old = scope.begin('room', 'old');
  assert.equal(
    scope.current(old, () => ({ room: 'room', token: 'new' })),
    false
  );
  assert.equal(
    scope.current(old, () => ({ room: 'other-room', token: 'old' })),
    false
  );
  const fresh = scope.begin('room', 'new');
  assert.equal(old.signal.aborted, true);
  assert.equal(
    scope.current(old, () => ({ room: 'room', token: 'old' })),
    false
  );
  assert.equal(
    scope.current(fresh, () => ({ room: 'room', token: 'new' })),
    true
  );
  scope.dispose();
  assert.equal(fresh.signal.aborted, true);
  assert.equal(
    scope.current(fresh, () => ({ room: 'room', token: 'new' })),
    false
  );
});
test('late completion safely checks a room prop cleared before component destruction', () => {
  const scope = new RoomRequestScope();
  const request = scope.begin('room', 'token');
  let room: { room_id: string } | null = { room_id: 'room' };
  const current = () => scope.current(request, () => ({ room: room?.room_id, token: 'token' }));
  assert.equal(current(), true);
  room = null;
  assert.equal(current(), false);
  scope.dispose();
  assert.equal(current(), false);
});
test('disposed request never evaluates getters from destroyed component props', () => {
  const scope = new RoomRequestScope();
  const request = scope.begin('room', 'token');
  let reads = 0;
  const identity = () => {
    reads++;
    return { room: 'room', token: 'token' };
  };
  assert.equal(scope.current(request, identity), true);
  scope.dispose();
  assert.equal(
    scope.current(request, () => {
      throw new Error('destroyed prop getter');
    }),
    false
  );
  assert.equal(reads, 1);
});
