import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AIQuoteState, appendAITheme, money, activeJob, normalizeAIRequest } from './ai-model.ts';
import { UsageRequestScope } from './usage-model.ts';
import type { PackDocument } from './types.ts';

test('quote drift and expiry prohibit new starts; ambiguous retry keeps original receipt', () => {
  const state = new AIQuoteState();
  state.set('quote', 'request', 2000);
  assert.equal(state.canStart('changed', 1000), false);
  assert.equal(state.canStart('request', 2000), false);
  assert.equal(state.canStart('request', 1999), true);
  const first = state.start('request', 1000, () => 'stable-key');
  assert.deepEqual(
    state.start('request', 3000, () => 'different'),
    first
  );
  state.clear();
  assert.throws(() => state.start('request', 1000, () => 'key'));
});
test('theme merge preserves unsaved text, rejects duplicate anywhere and final/missing rounds', () => {
  const doc = {
    name: 'Unsaved title',
    rounds: [
      { id: 'r', type: 'standard', themes: [] },
      { id: 'f', type: 'final', themes: [] }
    ]
  } as unknown as PackDocument;
  const preview = {
    rounds: [{ themes: [{ id: 'ai-theme', name: 'AI', questions: [] }] }]
  } as unknown as PackDocument;
  appendAITheme(doc, preview, 'r');
  assert.equal(doc.name, 'Unsaved title');
  assert.equal(doc.rounds[0].themes.length, 1);
  assert.throws(() => appendAITheme(doc, preview, 'r'));
  assert.throws(() => appendAITheme(doc, preview, 'f'));
  assert.throws(() => appendAITheme(doc, preview, 'missing'));
});
test('account replacement and unmount abort AI operations before callbacks', () => {
  const scope = new UsageRequestScope();
  const a = scope.begin('a:1');
  const b = scope.begin('a:2');
  assert.equal(a.signal.aborted, true);
  assert.equal(
    scope.current(a, () => 'a:2'),
    false
  );
  assert.equal(
    scope.current(b, () => 'b:2'),
    false
  );
  scope.dispose();
  assert.equal(b.signal.aborted, true);
  assert.equal(
    scope.current(b, () => {
      throw new Error('unmounted');
    }),
    false
  );
});
test('budget units and terminal jobs never imply polling or billing', () => {
  assert.equal(money(1500000), '$1.500000 USD');
  assert.equal(money(null), 'неизвестно');
  assert.equal(activeJob('uncertain'), false);
  assert.equal(activeJob('running'), true);
});

test('quote input matches server trimming without losing internal author text', () => {
  const input = {
    mode: 'pack' as const,
    topic: '  History  of science  ',
    audience: ' Adults ',
    difficulty: 3,
    themes: 1,
    questions_per_theme: 5
  };
  assert.deepEqual(normalizeAIRequest(input), {
    ...input,
    topic: 'History  of science',
    audience: 'Adults'
  });
  assert.equal(input.audience, ' Adults ');
});
