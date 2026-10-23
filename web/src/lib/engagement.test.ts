import test from 'node:test';
import assert from 'node:assert/strict';
import { ratingText, separateSourceRating, countWord } from './engagement.ts';
test('missing ratings are not zero stars', () => {
  assert.equal(ratingText(null, 0), 'Нет оценок');
  assert.equal(ratingText(4.25, 20), '4,3 / 5 · 20 оценок');
});
test('unknown or zero source vote count keeps rating separate', () => {
  assert.equal(separateSourceRating({ source_rating: 4.8, source_rating_count: null }), true);
  assert.equal(separateSourceRating({ source_rating: 4.8, source_rating_count: 0 }), true);
  assert.equal(separateSourceRating({ source_rating: 4.8, source_rating_count: 10 }), false);
  assert.equal(separateSourceRating({ source_rating: null, source_rating_count: null }), false);
});

test('Russian count labels handle units, teens and compound numbers',()=>{
 assert.equal(countWord(1,['оценка','оценки','оценок']),'оценка');
 assert.equal(countWord(2,['оценка','оценки','оценок']),'оценки');
 assert.equal(countWord(11,['оценка','оценки','оценок']),'оценок');
 assert.equal(countWord(21,['оценка','оценки','оценок']),'оценка');
 assert.equal(countWord(112,['оценка','оценки','оценок']),'оценок');
});
