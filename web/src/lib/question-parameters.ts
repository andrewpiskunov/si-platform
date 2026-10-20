import type { Question, Parameter } from './types.ts';
function replace(q: Question, name: string, parameter: Parameter | null): Question {
  const next = JSON.parse(JSON.stringify(q)) as Question;
  const index = next.params.findIndex((item) => item.name === name);
  if (!parameter) {
    if (index >= 0) next.params.splice(index, 1);
    return next;
  }
  if (index < 0) next.params.push(parameter);
  else {
    const {
      name: _name,
      type: _type,
      value: _value,
      items: _items,
      params: _params,
      number_set: _range,
      ...extensions
    } = next.params[index];
    next.params[index] = { ...extensions, ...parameter };
  }
  return next;
}
export function setSimpleParameter(q: Question, name: string, value: string): Question {
  return replace(q, name, value === '' ? null : { name, type: 'simple', value });
}
export function setPriceRange(
  q: Question,
  range: { minimum: number; maximum: number; step?: number } | null
): Question {
  return replace(
    q,
    'price',
    range ? { name: 'price', type: 'numberSet', number_set: range } : null
  );
}
export function setAnswerContent(q: Question, value: string): Question {
  const previous = q.params.find((p) => p.name === 'answer');
  const items = JSON.parse(JSON.stringify(previous?.items || []));
  const index = items.findIndex((item: { type: string }) => item.type === 'text');
  if (index < 0) items.push({ type: 'text', value });
  else items[index].value = value;
  return replace(q, 'answer', { name: 'answer', type: 'content', items });
}
