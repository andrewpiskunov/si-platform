import type {
  Card,
  Draft,
  Info,
  PackDocument,
  Parameter,
  Question,
  Round,
  Theme
} from './types.ts';

// Diagnostic drafts are intentionally editable before validation succeeds. Go's
// nil slices arrive as null; only known collection fields are normalized. Object
// spreads retain imported extensions, unsupported parameters and diagnostics.
function normalizeInfo(info: Info | null | undefined): Info {
  return { ...info, authors: info?.authors ?? [], sources: info?.sources ?? [] };
}
function normalizeParameter(parameter: Parameter): Parameter {
  return {
    ...parameter,
    items: parameter.items ?? [],
    params: (parameter.params ?? []).map(normalizeParameter)
  };
}
function normalizeQuestion(question: Question): Question {
  return {
    ...question,
    info: normalizeInfo(question.info),
    params: (question.params ?? []).map(normalizeParameter),
    right: question.right ?? [],
    wrong: question.wrong ?? []
  };
}
function normalizeTheme(theme: Theme): Theme {
  return {
    ...theme,
    info: normalizeInfo(theme.info),
    questions: (theme.questions ?? []).map(normalizeQuestion)
  };
}
function normalizeRound(round: Round): Round {
  return {
    ...round,
    info: normalizeInfo(round.info),
    themes: (round.themes ?? []).map(normalizeTheme)
  };
}
function normalizeDocument(document: PackDocument): PackDocument {
  return {
    ...document,
    info: normalizeInfo(document.info),
    files: document.files ?? [],
    tags: document.tags ?? [],
    import_issues: document.import_issues ?? [],
    rounds: (document.rounds ?? []).map(normalizeRound)
  };
}
export function normalizeDraft(draft: Draft): Draft {
  return {
    ...draft,
    document: normalizeDocument(draft.document),
    assets: draft.assets ?? [],
    issues: draft.issues ?? [],
    play_issues: draft.play_issues ?? []
  };
}
export function canWithdraw(
  card: Pick<Card, 'status' | 'published_revision' | 'pending_revision'>
): boolean {
  return card.published_revision > 0 || card.pending_revision > 0;
}
export function canPlayDraft(draft: Pick<Draft, 'issues' | 'play_issues'>): boolean {
  return ![...(draft.issues ?? []), ...(draft.play_issues ?? [])].some(
    (issue) => issue.severity === 'error'
  );
}
