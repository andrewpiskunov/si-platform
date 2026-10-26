import { test } from 'node:test';
import assert from 'node:assert/strict';
import { imageRevealProgress, revealProgress, revealText } from './reveal-model.ts';

const reveal = {
  kind: 'text' as const,
  start_at_ms: 10000,
  duration_ms: 4000,
  elapsed_ms: 1000,
  text_offset: 0
};
test('reveal catches up from authoritative snapshot time without restarting on reload', () => {
  assert.equal(revealProgress(reveal, 11000, 11000, false), 0.25);
  assert.equal(revealProgress(reveal, 11000, 12000, false), 0.5);
  assert.equal(revealProgress({ ...reveal, elapsed_ms: 2000 }, 12000, 12000, false), 0.5);
  assert.equal(revealProgress(reveal, 11000, 99000, false), 1);
  assert.equal(revealProgress(reveal, 11000, 10500, false), 0.25);
});
test('pause freezes snapshot progress; resume preserves elapsed rather than original wall-clock start', () => {
  assert.equal(revealProgress(reveal, 11000, 99000, true), 0.25);
  const resumed = { ...reveal, start_at_ms: 90000, elapsed_ms: 1000 };
  assert.equal(revealProgress(resumed, 91000, 92000, false), 0.5);
});
test('missing metadata keeps legacy content whole and completion clamps safely', () => {
  assert.equal(revealProgress(undefined, 0, 0, false), 1);
  assert.equal(revealProgress({ ...reveal, elapsed_ms: -100 }, 0, 0, true), 0);
  assert.equal(revealProgress({ ...reveal, duration_ms: 0 }, 0, 0, false), 1);
  assert.deepEqual(revealText('Legacy text', undefined, 0), {
    visible: 'Legacy text',
    concealed: ''
  });
});
test('reduced motion uses static quarter steps without granting content earlier', () => {
  for (const elapsed_ms of [0, 1, 999, 1000, 1500, 3999, 4000]) {
    const metadata = { ...reveal, elapsed_ms };
    const smooth = revealProgress(metadata, 11000, 11000, false);
    const reduced = revealProgress(metadata, 11000, 11000, false, true);
    assert.ok(reduced <= smooth);
    assert.equal(reduced * 4, Math.floor(reduced * 4));
  }
  assert.equal(revealProgress({ ...reveal, elapsed_ms: 3999 }, 11000, 11000, true, true), 0.75);
});
test('earlier public fragments stay whole while the current fragment reveals', () => {
  const text = 'Earlier\nABCD';
  const metadata = { ...reveal, text_offset: 8 };
  assert.deepEqual(revealText(text, metadata, 0), { visible: 'Earlier\n', concealed: 'ABCD' });
  assert.deepEqual(revealText(text, metadata, 0.5), { visible: 'Earlier\nAB', concealed: 'CD' });
  assert.deepEqual(revealText(text, metadata, 1), { visible: text, concealed: '' });
});
test('offsets use Unicode codepoints and reveal keeps combining and joined emoji graphemes intact', () => {
  const metadata = { ...reveal, text_offset: 2 };
  assert.deepEqual(revealText('😀\né👨‍👩‍👦', metadata, 0.5), { visible: '😀\né', concealed: '👨‍👩‍👦' });
});
test('image metadata does not partially mask accompanying text; invalid offsets never duplicate content', () => {
  assert.deepEqual(revealText('Caption', { ...reveal, kind: 'image' }, 0), {
    visible: 'Caption',
    concealed: ''
  });
  assert.deepEqual(revealText('AB', { ...reveal, text_offset: 99 }, 0), {
    visible: 'AB',
    concealed: ''
  });
  assert.deepEqual(revealText('AB', { ...reveal, text_offset: -4 }, 0.5), {
    visible: 'A',
    concealed: 'B'
  });
});

test('scheduled progressive image cannot flash whole before reveal snapshot reaches the client', () => {
  assert.equal(imageRevealProgress(undefined, true, 'scheduled_content', 10000, 12000, false), 0);
  assert.equal(imageRevealProgress(undefined, true, 'prepare_content', 10000, 12000, false), 0);
  assert.equal(imageRevealProgress({ ...reveal, kind: 'image' }, true, 'present_question', 11000, 11000, false), .25);
  assert.equal(imageRevealProgress(undefined, true, 'buzz_open', 11000, 12000, false), 1);
  assert.equal(imageRevealProgress(undefined, true, 'present_answer', 11000, 12000, false), 1);
  assert.equal(imageRevealProgress(undefined, false, 'scheduled_content', 10000, 12000, false), 1);
});
