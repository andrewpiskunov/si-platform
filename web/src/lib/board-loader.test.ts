import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BoardLoader } from './board-loader.ts';

test('a failed board remains retryable and only a successful key skips later snapshots', async () => {
  const loader = new BoardLoader<string>();
  let calls = 0;
  await assert.rejects(
    loader.load('room:lobby', async () => {
      calls++;
      throw new Error('503');
    }),
    /503/
  );
  assert.equal(
    await loader.load('room:lobby', async () => {
      calls++;
      return 'round options';
    }),
    'round options'
  );
  assert.equal(
    await loader.load('room:lobby', async () => {
      calls++;
      return 'duplicate';
    }),
    undefined
  );
  assert.equal(calls, 2);
  assert.equal(loader.pending, false);
});
test('concurrent snapshots share a board request, while a new room aborts and discards the old response', async () => {
  const loader = new BoardLoader<string>();
  let resolve!: (value: string) => void,
    signal!: AbortSignal,
    calls = 0;
  const first = loader.load('first:lobby', async (abort) => {
    calls++;
    signal = abort;
    return new Promise<string>((done) => (resolve = done));
  });
  const same = loader.load('first:lobby', async () => {
    calls++;
    return 'duplicate';
  });
  assert.equal(first, same);
  assert.equal(loader.pending, true);
  assert.equal(calls, 1);
  const next = loader.load('second:lobby', async () => 'second board');
  assert.equal(signal.aborted, true);
  resolve('stale board');
  assert.equal(await first, undefined);
  assert.equal(await next, 'second board');
  assert.equal(loader.pending, false);
});
test('explicit refresh reloads a successful board and leaving the room invalidates pending work', async () => {
  const loader = new BoardLoader<string>();
  assert.equal(await loader.load('room:lobby', async () => 'before'), 'before');
  assert.equal(await loader.load('room:lobby', async () => 'after', true), 'after');
  let resolve!: (value: string) => void;
  const pending = loader.load(
    'room:round',
    async () => new Promise<string>((done) => (resolve = done))
  );
  loader.reset();
  resolve('late result');
  assert.equal(await pending, undefined);
  assert.equal(loader.pending, false);
  assert.equal(await loader.load('room:lobby', async () => 'rejoined'), 'rejoined');
});

test('returning to a cached board cancels a different request still in flight', async () => {
  const loader = new BoardLoader<string>();
  await loader.load('current', async () => 'current board');
  let resolve!: (value: string) => void;
  const obsolete = loader.load(
    'other',
    async () => new Promise<string>((done) => (resolve = done))
  );
  assert.equal(await loader.load('current', async () => 'duplicate'), undefined);
  resolve('obsolete board');
  assert.equal(await obsolete, undefined);
  assert.equal(await loader.load('current', async () => 'duplicate'), undefined);
});
