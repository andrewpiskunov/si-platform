import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ServerClock,
  playbackPlan,
  CurrentResource,
  assetReference,
  readyAssets
} from './media-model.ts';
import { prepareAction, type SessionView } from './game-v2.ts';
test('media starts on server schedule, seeks after reconnect and freezes the paused position', () => {
  const clock = new ServerClock();
  clock.observe(10000, 2000);
  assert.equal(clock.now(2250), 10250);
  const media = { start_at_ms: 10750, paused_position_ms: 0, duration_ms: 8000 };
  assert.deepEqual(playbackPlan(media, false, clock.now(2250)), {
    show: false,
    play: false,
    position: 0,
    wait: 500
  });
  assert.deepEqual(playbackPlan(media, false, clock.now(6000)), {
    show: true,
    play: true,
    position: 3.25,
    wait: 0
  });
  assert.deepEqual(playbackPlan({ ...media, paused_position_ms: 2300 }, true, 99000), {
    show: true,
    play: false,
    position: 2.3,
    wait: 0
  });
  assert.equal(playbackPlan(media, false, 99000).position, 8);
  assert.equal(playbackPlan({ ...media, start_at_ms: 0 }, false, 99000).show, false);
});
test('stale ticket completion is invalidated and aborts when the fragment or stage changes', () => {
  const resource = new CurrentResource();
  const first = resource.begin('question-a:stage-1');
  const next = resource.begin('question-b:stage-2');
  assert.equal(first.signal.aborted, true);
  assert.equal(resource.current(first), false);
  assert.equal(resource.current(next), true);
  resource.close();
  assert.equal(next.signal.aborted, true);
  assert.equal(resource.current(next), false);
});
test('only prepared assets with the requested kind become SIQ references', () => {
  const prepared = {
    kind: 'image',
    duration_ms: 0,
    width: 1,
    height: 1,
    standard: { key: 'a', mime: 'image/png', size: 5 },
    economy: { key: 'b', mime: 'image/png', size: 4 }
  };
  const assets = [
    { name: 'Images/photo.png', key: 'source', size: 10, content_type: 'image/png', prepared },
    { name: 'Images/raw.svg', key: 'raw', size: 10, content_type: 'image/svg+xml' }
  ];
  assert.equal(assetReference(assets[0]), 'photo.png');
  assert.equal(readyAssets(assets, 'image').length, 1);
  assert.equal(readyAssets(assets, 'audio').length, 0);
});
test('media readiness is scoped, concurrent and has an exact status payload', () => {
  const view = {
    stage: { id: 's1', kind: 'prepare_content', instance_id: 'q1' },
    allowed_actions: [{ type: 'media_ready' }, { type: 'skip_question', requires_seq: true }]
  } as SessionView;
  assert.deepEqual(
    prepareAction(view, 13, 'media_ready', { status: 'ready' }, undefined, 'ready-1'),
    {
      v: 2,
      id: 'ready-1',
      type: 'media_ready',
      data: { stage_id: 's1', instance_id: 'q1', status: 'ready' }
    }
  );
  assert.throws(
    () => prepareAction(view, 13, 'media_ready', { status: 'done' }),
    /invalid_action_data/
  );
  assert.throws(() => prepareAction(view, 13, 'skip_question', {}), /invalid_action_data/);
  assert.equal(
    prepareAction(view, 13, 'skip_question', { reason: 'Файл не загрузился' }).expected_seq,
    13
  );
});
test('a paused scheduled fragment stays hidden even after its original start time passes', () => {
  const media = { start_at_ms: 10000, paused_position_ms: 0, duration_ms: 0 };
  assert.equal(playbackPlan(media, true, 15000, 'scheduled_content').show, false);
  assert.equal(playbackPlan(media, true, 15000, 'present_question').show, true);
});
