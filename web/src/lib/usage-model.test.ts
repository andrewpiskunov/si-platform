import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatUsageBytes, usageFailure, UsageRequestScope } from './usage-model.ts';
import type { Usage } from './types.ts';

test('storage formatting distinguishes unknown, empty and binary limits', () => {
  assert.equal(formatUsageBytes(undefined), 'Неизвестно');
  assert.equal(formatUsageBytes(0), '0 Б');
  assert.equal(formatUsageBytes(1), '1 Б');
  assert.equal(formatUsageBytes(5 * 1024 ** 2), '5 МиБ');
  assert.equal(formatUsageBytes(10 * 1024 ** 3), '10 ГиБ');
  assert.equal(formatUsageBytes(1536), '1,5 КиБ');
});

test('failed refresh preserves a stale snapshot; unauthorized clears private usage', () => {
  const snapshot = { storage: { used_bytes: 12, remaining_bytes: 0, limit_bytes: 12 } } as Usage;
  assert.deepEqual(usageFailure(snapshot, 503), { snapshot, stale: true });
  assert.deepEqual(usageFailure(null, 503), { snapshot: null, stale: false });
  assert.deepEqual(usageFailure(snapshot, 401), { snapshot: null, stale: false });
});

test('replaced requests and changed accounts cannot publish old usage', () => {
  const scope = new UsageRequestScope();
  const first = scope.begin('one');
  assert.equal(
    scope.current(first, () => 'two'),
    false
  );
  const second = scope.begin('two');
  assert.equal(first.signal.aborted, true);
  assert.equal(
    scope.current(first, () => {
      throw Error('obsolete getter');
    }),
    false
  );
  assert.equal(
    scope.current(second, () => 'two'),
    true
  );
  scope.dispose();
  assert.equal(second.signal.aborted, true);
  assert.equal(
    scope.current(second, () => {
      throw Error('destroyed getter');
    }),
    false
  );
});
