export function moveItem<T>(items: readonly T[], from: number, to: number): T[] {
  const next = [...items];
  if (
    !Number.isInteger(from) ||
    !Number.isInteger(to) ||
    from < 0 ||
    to < 0 ||
    from >= items.length ||
    to >= items.length ||
    from === to
  )
    return next;
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

// Resolve storage inside the guard: browsers can deny the sessionStorage getter
// itself, as well as reads and writes. Only a question ID is retained per pack.
export function readQuestionSelection(
  packID: string,
  questions: readonly { id: string }[],
  storage: () => Pick<Storage, 'getItem'> = () => sessionStorage
): string {
  let saved: string | null = null;
  try {
    saved = storage().getItem(`editor-selection:${packID}`);
  } catch {}
  return questions.find((question) => question.id === saved)?.id || questions[0]?.id || '';
}

export function saveQuestionSelection(
  packID: string,
  questionID: string,
  storage: () => Pick<Storage, 'setItem'> = () => sessionStorage
): void {
  try {
    storage().setItem(`editor-selection:${packID}`, questionID);
  } catch {}
}
