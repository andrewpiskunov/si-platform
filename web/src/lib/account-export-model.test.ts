import { test } from 'node:test';
import assert from 'node:assert/strict';
import { exportURL } from './account-export-model.ts';
import { UsageRequestScope } from './usage-model.ts';

test('whole-account download has no filter; metadata-only is explicit', () => {
  assert.equal(exportURL('all', '', false), '/api/privacy/export');
  assert.equal(exportURL('all', 'a'.repeat(32), true), '/api/privacy/export?metadata_only=true');
});

test('partial download requires a valid selected subject and preserves kind', () => {
  for (const kind of ['pack', 'theme', 'upload'] as const) {
    assert.equal(exportURL(kind, '', false), null);
    assert.equal(exportURL(kind, '../other', false), null);
    assert.equal(
      exportURL(kind, 'a'.repeat(32), true),
      `/api/privacy/export?kind=${kind}&id=${'a'.repeat(32)}&metadata_only=true`
    );
  }
});

test('picker guards obsolete kinds, new sessions of the same account, and destroyed getters', () => {
  const scope = new UsageRequestScope();
  const first = scope.begin('owner:1:pack');
  assert.equal(
    scope.current(first, () => 'owner:2:pack'),
    false
  );
  const second = scope.begin('owner:1:theme');
  assert.equal(
    scope.current(first, () => {
      throw Error('obsolete getter');
    }),
    false
  );
  assert.equal(
    scope.current(second, () => 'owner:1:theme'),
    true
  );
  scope.dispose();
  assert.equal(
    scope.current(second, () => {
      throw Error('destroyed getter');
    }),
    false
  );
});
