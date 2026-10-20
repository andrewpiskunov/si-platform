import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setSimpleParameter, setPriceRange, setAnswerContent } from './question-parameters.ts';
import type { Question } from './types.ts';
const original: Question = {
  id: 'q',
  price: 100,
  type: 'secret',
  info: { sources: ['Book'], extension: '<keep/>' },
  right: ['Answer'],
  params: [
    { name: 'unknown', type: 'simple', value: 'preserve' },
    { name: 'question', type: 'content', items: [{ type: 'text', value: 'Question' }] }
  ]
};
test('supported editor parameter updates preserve imported and unrelated question data', () => {
  const result = setSimpleParameter(original, 'selectionMode', 'exceptCurrent');
  assert.deepEqual(result.params[0], { name: 'unknown', type: 'simple', value: 'preserve' });
  assert.deepEqual(result.params[2], {
    name: 'selectionMode',
    type: 'simple',
    value: 'exceptCurrent'
  });
  assert.equal(original.params.length, 2);
  assert.deepEqual(result.info, original.info);
});
test('secret price range retains an explicit zero step and can be removed without replacing content', () => {
  const withRange = setPriceRange(original, { minimum: 100, maximum: 550, step: 0 });
  assert.deepEqual(withRange.params[2], {
    name: 'price',
    type: 'numberSet',
    number_set: { minimum: 100, maximum: 550, step: 0 }
  });
  assert.deepEqual(setPriceRange(withRange, null).params, original.params);
});
test('editing answer explanation changes only answer content, preserving correct answer variants', () => {
  const result = setAnswerContent(original, 'Explanation');
  assert.deepEqual(result.right, ['Answer']);
  assert.deepEqual(result.params.at(-1), {
    name: 'answer',
    type: 'content',
    items: [{ type: 'text', value: 'Explanation' }]
  });
  assert.equal(
    setSimpleParameter(result, 'answerDuration', '').params.some(
      (p) => p.name === 'answerDuration'
    ),
    false
  );
});
