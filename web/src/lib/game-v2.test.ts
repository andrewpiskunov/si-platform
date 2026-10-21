import { test } from 'node:test';
import assert from 'node:assert/strict';
import { prepareAction, captureScope, timerSeconds, type SessionView } from './game-v2.ts';
import { RoomProtocol } from './room-client.ts';
const view = (actions: SessionView['allowed_actions']): SessionView => ({
  version: 2,
  rules_version: 1,
  rules: {
    preset: 'classic',
    selection_seconds: 30,
    answer_seconds: 10,
    special_seconds: 30,
    wager_seconds: 30,
    final_seconds: 60,
    penalties: true,
    buzzer_mode: 'first',
    window_ms: 200,
    false_start: true,
    false_start_seconds: 3,
    no_risk_multiplier: 2
  },
  chooser_id: 'player-1',
  server_time_ms: 10000,
  stage: {
    id: 'stage-4',
    kind: 'collect_wagers',
    instance_id: 'question-2',
    deadline_ms: 40000,
    expired: false
  },
  players: [],
  self: {},
  submissions: [],
  decisions: [],
  appeals: [],
  allowed_actions: actions
});
test('a private wager sends only its amount and captured stage, without global sequence', () => {
  const game = view([{ type: 'wager', minimum: 1, maximum: 500 }]);
  assert.deepEqual(
    prepareAction(game, 18, 'wager', { amount: 300 }, captureScope(game), 'wager-1'),
    {
      v: 2,
      id: 'wager-1',
      type: 'wager',
      data: { stage_id: 'stage-4', instance_id: 'question-2', amount: 300 }
    }
  );
  assert.throws(
    () => prepareAction(game, 18, 'wager', { amount: 300, target_id: 'other' }),
    /invalid_action_data/
  );
  assert.throws(() => prepareAction(game, 18, 'wager', { amount: 501 }), /invalid_amount/);
});
test('a form captured on a previous stage cannot submit on the replacement stage', () => {
  const game = view([{ type: 'answer' }]);
  const captured = captureScope(game);
  game.stage = { ...game.stage, id: 'stage-5' };
  assert.throws(
    () => prepareAction(game, 19, 'answer', { text: 'Local draft' }, captured),
    /stale_stage/
  );
});
test('absent actions and unadvertised targets or modes cannot become commands', () => {
  const game = view([
    { type: 'select_recipient', targets: ['player-2'] },
    { type: 'bid', modes: ['pass', 'all_in'], minimum: 100, maximum: 450 }
  ]);
  assert.throws(
    () => prepareAction(game, 1, 'judge', { target_id: 'player-2', correct: true }),
    /action_unavailable/
  );
  assert.throws(
    () => prepareAction(game, 1, 'select_recipient', { target_id: 'player-3' }),
    /invalid_target/
  );
  assert.throws(() => prepareAction(game, 1, 'bid', { mode: 'bid', amount: 100 }), /invalid_mode/);
  assert.throws(
    () => prepareAction(game, 1, 'begin_question', { question: {} }),
    /action_unavailable/
  );
  assert.deepEqual(prepareAction(game, 1, 'bid', { mode: 'all_in' }, undefined, 'all-in').data, {
    stage_id: 'stage-4',
    instance_id: 'question-2',
    mode: 'all_in'
  });
});
test('server advertised amount grid allows the exact maximum and sequence is opt-in', () => {
  const game = view([
    { type: 'select_price', minimum: 100, maximum: 550, step: 200, requires_seq: true }
  ]);
  const command = prepareAction(game, 9, 'select_price', { amount: 550 }, undefined, 'price-1');
  assert.equal(command.expected_seq, 9);
  assert.equal(command.data.amount, 550);
  assert.throws(() => prepareAction(game, 9, 'select_price', { amount: 200 }), /invalid_amount/);
});
test('expired clock display does not remove a server-authorized recovery action', () => {
  const game = view([{ type: 'extend', minimum: 5, maximum: 120, requires_seq: true }]);
  game.stage.expired = true;
  assert.deepEqual(prepareAction(game, 20, 'extend', { seconds: 15 }, undefined, 'extend-1'), {
    v: 2,
    id: 'extend-1',
    type: 'extend',
    expected_seq: 20,
    data: { stage_id: 'stage-4', seconds: 15 }
  });
});
test('the display timer uses server time and freezes paused remaining time', () => {
  assert.equal(
    timerSeconds({ id: 's', kind: 'judge', deadline_ms: 20000, expired: false }, 10000, 2500),
    8
  );
  assert.equal(
    timerSeconds(
      { id: 's', kind: 'judge', remaining_ms: 4500, expired: false },
      10000,
      99999,
      true
    ),
    5
  );
  assert.equal(
    timerSeconds({ id: 's', kind: 'judge', deadline_ms: 9000, expired: true }, 10000),
    0
  );
});
test('v2 pending commands reconnect with the same ID and private payload', () => {
  const game = view([{ type: 'wager', minimum: 1, maximum: 500 }]);
  const protocol = new RoomProtocol('token');
  const state = {
    room_id: 'r',
    seq: 2,
    phase: 'running' as const,
    participants: [],
    you: 'player-1',
    gameplay: game
  };
  protocol.receive({ v: 1, type: 'snapshot', state });
  const command = prepareAction(game, 2, 'wager', { amount: 250 }, undefined, 'private-wager');
  protocol.commandV2(command);
  protocol.disconnected();
  protocol.connected();
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: { ...state, seq: 3 } }), [
    command
  ]);
  protocol.receive({
    v: 1,
    type: 'result',
    command_id: 'private-wager',
    code: 'ok',
    state: { ...state, seq: 4 }
  });
  assert.equal(protocol.pending.size, 0);
});

test('auction steps use absolute multiples while price choices use their minimum', () => {
  const game = view([{ type: 'bid', minimum: 101, maximum: 550, step: 100, modes: ['bid'] }]);
  assert.equal(prepareAction(game, 1, 'bid', { mode: 'bid', amount: 101 }).data.amount, 101);
  assert.equal(prepareAction(game, 1, 'bid', { mode: 'bid', amount: 200 }).data.amount, 200);
  assert.throws(
    () => prepareAction(game, 1, 'bid', { mode: 'bid', amount: 201 }),
    /invalid_amount/
  );
  assert.equal(prepareAction(game, 1, 'bid', { mode: 'bid', amount: 550 }).data.amount, 550);
});
test('a stage kind change invalidates a captured form even if stage ID is stable', () => {
  const game = view([{ type: 'buzz' }]);
  game.stage.kind = 'buzz_open';
  const captured = captureScope(game);
  game.stage.kind = 'buzz_window';
  assert.throws(() => prepareAction(game, 1, 'buzz', {}, captured), /stale_stage/);
});
test('submitted answers are limited by UTF-8 bytes and never silently truncated', () => {
  const game = view([{ type: 'answer' }]);
  assert.equal(
    prepareAction(game, 1, 'answer', { text: 'я'.repeat(512) }).data.text,
    'я'.repeat(512)
  );
  assert.throws(
    () => prepareAction(game, 1, 'answer', { text: 'я'.repeat(513) }),
    /answer_too_long/
  );
  assert.throws(
    () => prepareAction(game, 1, 'answer', { text: '🎲'.repeat(257) }),
    /answer_too_long/
  );
});
test('serialized answer budget includes escaped characters and reasons use UTF-8 limits', () => {
  const game = view([{ type: 'answer' }, { type: 'restart_stage' }]);
  assert.throws(
    () => prepareAction(game, 1, 'answer', { text: '<'.repeat(171) }),
    /answer_too_long/
  );
  assert.equal(
    prepareAction(game, 1, 'answer', { text: '<'.repeat(170) }).data.text,
    '<'.repeat(170)
  );
  assert.throws(
    () => prepareAction(game, 1, 'restart_stage', { reason: 'я'.repeat(513) }),
    /reason_too_long/
  );
});
test('removal sends only selected target and valid mode under sequence control', () => {
  const current = view([{ type: 'remove_participant', targets: ['guest'], requires_seq: true }]);
  const command = prepareAction(current, 12, 'remove_participant', {
    target_id: 'guest',
    mode: 'ban_guest_session'
  });
  assert.deepEqual(command.data, { target_id: 'guest', mode: 'ban_guest_session' });
  assert.equal(command.expected_seq, 12);
  assert.throws(
    () => prepareAction(current, 12, 'remove_participant', { target_id: 'host', mode: 'kick' }),
    /invalid_target/
  );
  assert.throws(
    () => prepareAction(current, 12, 'remove_participant', { target_id: 'guest', mode: 'ban_ip' }),
    /invalid_mode/
  );
  assert.throws(
    () =>
      prepareAction(current, 12, 'remove_participant', {
        target_id: 'guest',
        mode: 'kick',
        targets: ['other']
      }),
    /invalid_action_data/
  );
});

test('lobby readiness is own boolean intent without global sequence or question scope', () => {
  const game = view([{ type: 'set_ready', requires_seq: false }]);
  assert.deepEqual(prepareAction(game, 18, 'set_ready', { ready: true }, captureScope(game), 'ready-1'), {
    v: 2, id: 'ready-1', type: 'set_ready', data: { ready: true }
  });
  assert.throws(() => prepareAction(game, 18, 'set_ready', {}));
  assert.throws(() => prepareAction(game, 18, 'set_ready', {ready: null}));
  assert.throws(() => prepareAction(game, 18, 'set_ready', {ready: true, target_id: 'other'}));
});
