import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalizeDraft, canPlayDraft, canWithdraw } from './draft-model.ts';
import type { Draft } from './types.ts';

test('a diagnostic draft with null rounds remains editable without changing the response', () => {
  const raw = {
    document: { name: 'Repair me', rounds: null, info: null },
    assets: null,
    issues: [{ severity: 'error', code: 'required', path: 'rounds', message: 'Нужен раунд' }]
  } as unknown as Draft;
  const draft = normalizeDraft(raw);
  assert.deepEqual(draft.document.rounds, []);
  assert.deepEqual(draft.document.info.authors, []);
  assert.deepEqual(draft.assets, []);
  assert.equal(draft.issues?.[0].code, 'required');
  assert.equal(raw.document.rounds, null);
});

test('normalization reaches nullable nested collections while preserving unknown fields and diagnostics', () => {
  const raw = {
    custom_draft_field: { keep: null },
    play_issues: [
      { severity: 'error', code: 'unsupported_round', path: 'rounds[0]', message: 'Финал' }
    ],
    document: {
      name: 'Imported',
      version: '5.0',
      info: { authors: null, sources: null, extension: '<custom />' },
      import_issues: [
        { severity: 'error', code: 'unsupported_script', path: 'rounds[0]', message: 'Сценарий' }
      ],
      custom_document_field: { keep: null },
      rounds: [
        {
          id: 'r',
          name: 'Final',
          type: 'final',
          info: null,
          custom_round_field: 42,
          themes: [
            {
              id: 't',
              name: 'Theme',
              info: { authors: null, sources: null },
              questions: [
                {
                  id: 'q',
                  price: 100,
                  info: null,
                  right: null,
                  wrong: null,
                  has_script: true,
                  custom_question_field: 'keep',
                  params: [
                    {
                      name: 'unsupported',
                      type: 'group',
                      params: null,
                      items: null,
                      custom_parameter_field: true
                    },
                    {
                      name: 'nested',
                      type: 'group',
                      params: [{ name: 'question', type: 'content', items: null }],
                      items: null
                    }
                  ]
                },
                { id: 'q2', params: null, right: null, info: null }
              ]
            },
            { id: 'empty', questions: null, info: null }
          ]
        },
        { id: 'empty-round', themes: null, info: null }
      ]
    },
    assets: null,
    issues: null
  } as unknown as Draft;
  const draft = normalizeDraft(raw);
  const question = draft.document.rounds[0].themes[0].questions[0];
  assert.deepEqual(question.right, []);
  assert.deepEqual(question.wrong, []);
  assert.deepEqual(question.info.authors, []);
  assert.deepEqual(question.params[0].params, []);
  assert.deepEqual(question.params[0].items, []);
  assert.deepEqual(question.params[1].params?.[0].items, []);
  assert.deepEqual(draft.document.rounds[0].themes[0].questions[1].params, []);
  assert.deepEqual(draft.document.rounds[0].themes[1].questions, []);
  assert.deepEqual(draft.document.rounds[1].themes, []);
  assert.equal(question.has_script, true);
  assert.equal(draft.document.rounds[0].type, 'final');
  assert.deepEqual(draft.document.import_issues, raw.document.import_issues);
  assert.deepEqual(draft.play_issues, raw.play_issues);
  assert.equal(draft.document.info.extension, '<custom />');
  const preserved = JSON.parse(JSON.stringify(draft));
  assert.deepEqual(preserved.custom_draft_field, { keep: null });
  assert.deepEqual(preserved.document.custom_document_field, { keep: null });
  assert.equal(preserved.document.rounds[0].custom_round_field, 42);
  assert.equal(preserved.document.rounds[0].themes[0].questions[0].custom_question_field, 'keep');
  assert.equal(
    preserved.document.rounds[0].themes[0].questions[0].params[0].custom_parameter_field,
    true
  );
});

test('an edited draft can withdraw an earlier publication or pending review', () => {
  assert.equal(canWithdraw({ status: 'draft', published_revision: 3, pending_revision: 0 }), true);
  assert.equal(canWithdraw({ status: 'draft', published_revision: 0, pending_revision: 7 }), true);
  assert.equal(
    canWithdraw({ status: 'published', published_revision: 0, pending_revision: 0 }),
    false
  );
});

test('play readiness considers engine errors independently of SIQ validation', () => {
  const validSIQ = {
    issues: [],
    play_issues: [
      { severity: 'error', code: 'unsupported_round', path: 'rounds[0]', message: 'Финал' }
    ]
  };
  assert.equal(canPlayDraft(validSIQ), false);
  assert.deepEqual(validSIQ.issues, []);
  assert.equal(
    canPlayDraft({
      issues: [{ severity: 'error', code: 'required', path: 'name', message: 'Название' }],
      play_issues: []
    }),
    false
  );
  assert.equal(
    canPlayDraft({
      issues: [],
      play_issues: [{ severity: 'warning', code: 'warning', path: '', message: 'Примечание' }]
    }),
    true
  );
});
