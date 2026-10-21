import { test } from 'node:test';
import assert from 'node:assert/strict';
import { removalTargets } from './room-participants.ts';
import type { SessionView } from './game-v2.ts';
test('removal targets intersect active non-host membership with server authorization', () => {
  const participants = [
    { id: 'host', name: 'H', role: 'host' as const, score: 0 },
    { id: 'active', name: 'A', role: 'player' as const, score: 10 },
    { id: 'removed', name: 'R', role: 'player' as const, score: 20, removed: true },
    { id: 'spectator', name: 'S', role: 'spectator' as const, score: 0 },
    { id: 'other', name: 'O', role: 'player' as const, score: 0 }
  ];
  const view = {
    allowed_actions: [
      { type: 'remove_participant', targets: ['host', 'active', 'removed', 'spectator'] }
    ]
  } as SessionView;
  assert.deepEqual(
    removalTargets(participants, 'host', view).map((p) => p.id),
    ['active', 'spectator']
  );
  assert.deepEqual(removalTargets(participants, 'active', view), []);
});
