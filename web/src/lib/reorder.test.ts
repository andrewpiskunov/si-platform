import { test } from 'node:test';
import assert from 'node:assert/strict';
import { moveItem, readQuestionSelection, saveQuestionSelection } from './reorder.ts';
import { DraftEditor } from './draft-editor.ts';
import { normalizeDraft } from './draft-model.ts';
import type { Content, Draft, PackDocument, Parameter } from './types.ts';

type ImportedParameter = Omit<Parameter, 'items'> & {
  imported?: string;
  items?: (Content & { imported?: string })[];
};

function fixture() {
  const question = (id: string) => ({
    id,
    price: 100,
    type: 'secret',
    info: { extension: '<private/>', sources: ['source'] },
    right: ['answer', 'variant'],
    wrong: ['wrong'],
    imported: { nested: ['kept'] },
    params: [
      {
        name: 'question',
        type: 'content',
        imported: 'question metadata',
        items: [
          { type: 'text', value: 'First', imported: 'first' },
          {
            type: 'image',
            value: 'Images/picture.png',
            is_ref: true,
            placement: 'background',
            duration_ms: 1200,
            imported: 'media'
          },
          { type: 'audio', value: 'Audio/clip.mp3', is_ref: true, wait_for_finish: true }
        ]
      },
      {
        name: 'answer',
        type: 'content',
        items: [
          { type: 'text', value: 'Explanation' },
          { type: 'video', value: 'Video/explanation.mp4', is_ref: true, imported: 'answer media' }
        ]
      },
      { name: 'future-parameter', type: 'future', value: 'preserved' }
    ] as ImportedParameter[]
  });
  return {
    id: 'pack',
    name: 'Pack',
    version: '5',
    info: {},
    global_xml: '<extension/>',
    import_issues: [{ severity: 'warning', code: 'future', path: 'rounds', message: 'Keep' }],
    rounds: [
      {
        id: 'round-a',
        name: 'A',
        info: {},
        imported: 'round metadata',
        themes: [
          {
            id: 'theme-a',
            name: 'A',
            info: {},
            imported: 'theme metadata',
            questions: [question('q-a'), question('q-b')]
          },
          { id: 'theme-b', name: 'B', info: {}, questions: [question('q-c')] }
        ]
      },
      { id: 'round-b', name: 'B', info: {}, themes: [] }
    ]
  };
}

test('moving an item keeps object identities and never mutates a readonly source', () => {
  const items = Object.freeze(
    [{ id: 'a' }, { id: 'b' }, { id: 'c' }].map((item) => Object.freeze(item))
  );
  const moved = moveItem(items, 0, 2);
  assert.deepEqual(
    moved.map((item) => item.id),
    ['b', 'c', 'a']
  );
  assert.equal(moved[2], items[0]);
  assert.deepEqual(moveItem(moved, 2, 0), items);
  assert.deepEqual(
    items.map((item) => item.id),
    ['a', 'b', 'c']
  );
  assert.notEqual(moved, items);
});

test('invalid, equal and boundary indexes preserve order without mutating the source', () => {
  const items = Object.freeze(['a', 'b']);
  for (const [from, to] of [
    [-1, 0],
    [0, -1],
    [2, 0],
    [0, 2],
    [0.5, 1],
    [0, 0.5],
    [NaN, 1],
    [0, Infinity],
    [1, 1]
  ]) {
    const moved = moveItem(items, from, to);
    assert.deepEqual(moved, ['a', 'b']);
    assert.notEqual(moved, items);
  }
  assert.deepEqual(moveItem([], 0, 1), []);
});

test('moving rounds, themes and questions preserves IDs, imported fields and media references', () => {
  const original = fixture(),
    next = structuredClone(original);
  next.rounds = moveItem(next.rounds, 0, 1);
  next.rounds[1].themes = moveItem(next.rounds[1].themes, 0, 1);
  next.rounds[1].themes[1].questions = moveItem(next.rounds[1].themes[1].questions, 0, 1);
  assert.deepEqual(
    next.rounds.map((round) => round.id),
    ['round-b', 'round-a']
  );
  assert.deepEqual(
    next.rounds[1].themes.map((theme) => theme.id),
    ['theme-b', 'theme-a']
  );
  assert.deepEqual(
    next.rounds[1].themes[1].questions.map((question) => question.id),
    ['q-b', 'q-a']
  );
  assert.deepEqual(
    next.rounds[1].themes[1].questions[1],
    original.rounds[0].themes[0].questions[0]
  );
  assert.equal(next.rounds[1].imported, 'round metadata');
  assert.equal(next.rounds[1].themes[1].imported, 'theme metadata');
  assert.deepEqual(next.import_issues, original.import_issues);
  assert.equal(next.global_xml, original.global_xml);
  assert.equal(original.rounds[0].themes[0].questions[0].id, 'q-a');
});

test('question and answer fragment order changes independently while extensions and timing survive', () => {
  const original = fixture().rounds[0].themes[0].questions[0],
    next = structuredClone(original);
  next.params[0].items = moveItem(next.params[0].items!, 0, 2);
  next.params[1].items = moveItem(next.params[1].items!, 1, 0);
  assert.deepEqual(
    next.params[0].items!.map((item) => item.value),
    ['Images/picture.png', 'Audio/clip.mp3', 'First']
  );
  assert.deepEqual(
    next.params[1].items!.map((item) => item.value),
    ['Video/explanation.mp4', 'Explanation']
  );
  assert.deepEqual(next.params[0].items![0], original.params[0].items![1]);
  assert.deepEqual(next.params[1].items![0], original.params[1].items![1]);
  assert.equal(next.params[0].imported, 'question metadata');
  assert.deepEqual(next.params[2], original.params[2]);
  assert.deepEqual(next.right, original.right);
});

test('one reorder creates one undo entry and undo restores the complete prior document', () => {
  const original = fixture(),
    editor = new DraftEditor(original, 3);
  const next = structuredClone(editor.document);
  next.rounds = moveItem(next.rounds, 0, 1);
  editor.edit(next);
  assert.equal(editor.document.rounds[1].themes[0].questions[0].id, 'q-a');
  assert.equal(editor.dirty, true);
  editor.undo();
  assert.deepEqual(editor.document, original);
  assert.equal(editor.canUndo, false);
  assert.deepEqual(editor.beginSave()?.document, original);
});

test('a stale save completion cannot replace a later reorder or its undo history', () => {
  const original = fixture(),
    editor = new DraftEditor(original, 3);
  editor.edit({ ...editor.document, name: 'Edited title' });
  const previous = editor.beginSave()!;
  const next = structuredClone(editor.document);
  next.rounds[0].themes[0].questions = moveItem(next.rounds[0].themes[0].questions, 0, 1);
  editor.edit(next);
  editor.saved(previous, previous.document, 4);
  assert.equal(editor.dirty, true);
  const save = editor.beginSave()!;
  assert.equal(save.base_revision, 4);
  assert.deepEqual(
    save.document.rounds[0].themes[0].questions.map((question) => question.id),
    ['q-b', 'q-a']
  );
  assert.deepEqual(
    save.document.rounds[0].themes[0].questions[1],
    original.rounds[0].themes[0].questions[0]
  );
  editor.undo();
  editor.saved(save, save.document, 5);
  assert.deepEqual(
    editor.document.rounds[0].themes[0].questions.map((question) => question.id),
    ['q-a', 'q-b']
  );
  assert.equal(editor.document.name, 'Edited title');
  assert.equal(editor.dirty, true);
  assert.equal(editor.beginSave()?.base_revision, 5);
});

test('normalized diagnostic drafts retain empty collections and unknown fields after a move', () => {
  const document = {
    ...fixture(),
    rounds: [
      { id: 'empty', name: 'Empty', themes: null, imported: 'keep' },
      {
        id: 'diagnostic',
        name: 'Diagnostic',
        themes: [{ id: 'empty-theme', name: 'Empty', questions: null }]
      }
    ]
  };
  const normalized = normalizeDraft({
    document: document as unknown as PackDocument
  } as Draft).document;
  const next = { ...normalized, rounds: moveItem(normalized.rounds, 0, 1) };
  assert.deepEqual(next.rounds[0].themes[0].questions, []);
  assert.deepEqual(next.rounds[1].themes, []);
  assert.equal((next.rounds[1] as unknown as { imported: string }).imported, 'keep');
  assert.deepEqual(next.import_issues, document.import_issues);
});

test('selection persistence stores only question IDs, separates packs and restores after parent moves', () => {
  const values = new Map<string, string>();
  const storage = () => ({
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    }
  });
  saveQuestionSelection('pack-a', 'q-a', storage);
  saveQuestionSelection('pack-b', 'q-b', storage);
  const questions = [{ id: 'q-b' }, { id: 'q-a' }];
  assert.equal(readQuestionSelection('pack-a', questions, storage), 'q-a');
  assert.equal(readQuestionSelection('pack-b', questions, storage), 'q-b');
  assert.equal(readQuestionSelection('pack-c', questions, storage), 'q-b');
  assert.deepEqual([...values.values()], ['q-a', 'q-b']);
});

test('stale or deleted selected IDs fall back to the first existing question and empty drafts stay empty', () => {
  const stale = () => ({ getItem: () => 'deleted-question' });
  assert.equal(readQuestionSelection('pack', [{ id: 'first' }, { id: 'next' }], stale), 'first');
  assert.equal(readQuestionSelection('pack', [], stale), '');
});

test('unavailable storage or denied access cannot break editor selection or edits', () => {
  const unavailable = () => {
    throw new Error('storage unavailable');
  };
  const denied = () => ({
    getItem: () => {
      throw new Error('access denied');
    },
    setItem: () => {
      throw new Error('access denied');
    }
  });
  for (const storage of [unavailable, denied]) {
    assert.equal(readQuestionSelection('pack', [{ id: 'first' }], storage), 'first');
    assert.doesNotThrow(() => saveQuestionSelection('pack', 'first', storage));
  }
});
