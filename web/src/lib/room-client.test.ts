import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RoomProtocol } from './room-client.ts';
const state = (seq: number) => ({
  room_id: 'room',
  seq,
  phase: 'open' as const,
  participants: [],
  you: 'me'
});
test('reconnect authenticates before replaying the exact unacknowledged command', () => {
  const protocol = new RoomProtocol('token');
  assert.deepEqual(protocol.connected(), { v: 1, type: 'authenticate', token: 'token' });
  protocol.receive({ v: 1, type: 'snapshot', state: state(2) });
  const command = protocol.command('buzz', { question_id: 'q' }, 'attempt-1');
  protocol.disconnected();
  protocol.connected();
  assert.equal(protocol.ready, false);
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(4) }), [command]);
  assert.deepEqual(command, { v: 1, id: 'attempt-1', type: 'buzz', question_id: 'q' });
});
test('an older acknowledgement clears pending without replacing a newer snapshot', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(8) });
  protocol.command('buzz', { question_id: 'q' }, 'attempt-1');
  protocol.receive({
    v: 1,
    type: 'result',
    command_id: 'attempt-1',
    code: 'ok',
    result_seq: 3,
    duplicate: true,
    state: state(3)
  });
  assert.equal(protocol.state?.seq, 8);
  assert.equal(protocol.pending.size, 0);
  protocol.disconnected();
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(9) }), []);
});
test('same command ID cannot silently change its payload', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(1) });
  const first = protocol.command('judge', { question_id: 'q', correct: true }, 'judge-1');
  assert.deepEqual(
    protocol.command('judge', { question_id: 'q', correct: true }, 'judge-1'),
    first
  );
  assert.throws(
    () => protocol.command('judge', { question_id: 'q', correct: false }, 'judge-1'),
    /command_conflict/
  );
});
test('transport failure keeps an uncertain command available for retry', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(1) });
  const command = protocol.command('pause', {}, 'pause-1');
  protocol.receive({ v: 1, type: 'error', command_id: 'pause-1', code: 'unavailable' });
  protocol.disconnected();
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(2) }), [command]);
});
