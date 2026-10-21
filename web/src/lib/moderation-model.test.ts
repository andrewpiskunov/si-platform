import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reasonError, canResolve, mergePage, previewKey, RequestGate } from './moderation-model.ts';

test('mandatory reasons reject whitespace, oversized and control text but allow multiline unicode', () => {
  for (const value of ['  ', 'a'.repeat(2001), 'bad\u0000text', 'bad\u007ftext'])
    assert.notEqual(reasonError(value), '');
  assert.equal(reasonError('Причина\nподробнее\t🙂'), '');
  assert.equal(reasonError('', false), '');
  assert.equal(reasonError('🙂'.repeat(2000)), '');
});
test('terminal reports and in-flight actions cannot be resolved again', () => {
  assert.equal(canResolve('resolved', false), false);
  assert.equal(canResolve('open', true), false);
  assert.equal(canResolve('open', false), true);
});
test('page append deduplicates overlapping entries while refresh replaces stale entries', () => {
  assert.deepEqual(mergePage([{ id: 1 }, { id: 2 }], [{ id: 2 }, { id: 3 }], true), [
    { id: 1 },
    { id: 2 },
    { id: 3 }
  ]);
  assert.deepEqual(mergePage([{ id: 1 }], null, false), []);
});
test('media identity changes for every pack, revision or question change', () => {
  const keys = [
    ['a', 1, 'q'],
    ['b', 1, 'q'],
    ['a', 2, 'q'],
    ['a', 1, 'r']
  ].map(([id, revision, q]) => previewKey(String(id), Number(revision), String(q)));
  assert.equal(new Set(keys).size, 4);
});
test('late and disposed requests never evaluate component getters in callbacks', () => {
  const gate = new RequestGate();
  const old = gate.start(),
    current = gate.start();
  let visible = 'new';
  gate.apply(old, () => {
    visible = 'old';
  });
  assert.equal(visible, 'new');
  gate.apply(current, () => {
    visible = 'current';
  });
  assert.equal(visible, 'current');
  gate.dispose();
  gate.apply(current, () => {
    throw new Error('destroyed Svelte getter accessed');
  });
});
