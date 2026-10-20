import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DraftEditor } from './draft-editor.ts';
test('an in-flight save never replaces edits typed after the request', () => {
  const editor = new DraftEditor({ name: 'First' }, 1);
  editor.edit({ name: 'Sent' });
  const request = editor.beginSave();
  editor.edit({ name: 'Typed meanwhile' });
  editor.saved(request!, { name: 'Sent' }, 2);
  assert.equal(editor.document.name, 'Typed meanwhile');
  assert.equal(editor.revision, 2);
  assert.equal(editor.dirty, true);
  assert.deepEqual(editor.beginSave()?.document, { name: 'Typed meanwhile' });
});
test('a save conflict stops autosave and preserves local draft and undo history', () => {
  const editor = new DraftEditor({ name: 'Original' }, 3);
  editor.edit({ name: 'Local' });
  editor.beginSave();
  editor.failed('conflict');
  assert.equal(editor.document.name, 'Local');
  assert.equal(editor.dirty, true);
  assert.equal(editor.beginSave(), null);
  editor.rebase(7);
  assert.equal(editor.beginSave()?.base_revision, 7);
  editor.failed('unavailable');
  editor.undo();
  assert.equal(editor.document.name, 'Original');
});
test('successful unchanged save clears dirty state and forbids concurrent saves', () => {
  const editor = new DraftEditor({ name: 'Original' }, 1);
  editor.edit({ name: 'New' });
  const request = editor.beginSave();
  assert.equal(editor.beginSave(), null);
  editor.saved(request!, { name: 'New' }, 2);
  assert.equal(editor.dirty, false);
});
