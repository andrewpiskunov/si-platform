import { test } from 'node:test';
import assert from 'node:assert/strict';
import { get } from 'svelte/store';
import { createRoomTheme } from './room-theme.ts';

test('session theme state is isolated between independently rendered application shells', () => {
  const first = createRoomTheme();
  const second = createRoomTheme();
  first.select('pirate');
  assert.equal(get(first), 'pirate');
  assert.equal(get(second), null);
  second.select('anime');
  assert.equal(get(first), 'pirate');
  assert.equal(get(second), 'anime');
});

test('leaving a room removes the session theme and unknown themes use classic', () => {
  const theme = createRoomTheme();
  theme.select('anime');
  theme.reset();
  assert.equal(get(theme), null);
  theme.select('unknown');
  assert.equal(get(theme), 'classic');
});

test('a delayed manifest from a previous room never overrides the new room', async (t) => {
  let respond!: (value: Response) => void;
  t.mock.method(globalThis, 'fetch', () => new Promise<Response>((resolve) => (respond = resolve)));
  const theme = createRoomTheme();
  theme.enter('a'.repeat(32), 'token-a', { classic: false, economy: false });
  const first = theme.refresh();
  theme.enter('b'.repeat(32), 'token-b', { classic: false, economy: true });
  respond(Response.json({ appearance: { builtin: 'pirate' }, status: 'ready', assets: [] }));
  await first;
  assert.equal(get(theme.detail).room, 'b'.repeat(32));
  assert.equal(get(theme), 'classic');
  assert.equal(get(theme.detail).preferences.economy, true);
  theme.reset();
});

test('unrelated game snapshots reuse appearance and refresh only on an epoch or connection change', async (t) => {
  let requests = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    requests++;
    return Response.json({ appearance: { builtin: 'anime' }, status: 'ready', assets: [] });
  });
  const theme = createRoomTheme();
  theme.enter('a'.repeat(32), 'token', { classic: false, economy: false });
  theme.observe({ builtin: 'anime' }, 0, true);
  await new Promise((resolve) => setTimeout(resolve, 0));
  for (let i = 0; i < 12; i++) theme.observe({ builtin: 'anime' }, 0, true);
  assert.equal(requests, 1);
  assert.equal(get(theme), 'anime');
  theme.observe({ builtin: 'anime' }, 1, true);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(requests, 2);
  theme.observe({ builtin: 'anime' }, 1, false);
  theme.observe({ builtin: 'anime' }, 1, true);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(requests, 3);
  theme.reset();
});
