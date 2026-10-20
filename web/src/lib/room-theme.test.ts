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
