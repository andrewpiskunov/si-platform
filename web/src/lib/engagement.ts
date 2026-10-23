export function ratingText(value: number | null, count: number): string {
  return value == null
    ? 'Нет оценок'
    : `${value.toLocaleString('ru-RU', { minimumFractionDigits: 1, maximumFractionDigits: 1 })} / 5 · ${count.toLocaleString('ru-RU')} ${countWord(count,['оценка','оценки','оценок'])}`;
}
export function separateSourceRating(e: {
  source_rating: number | null;
  source_rating_count: number | null;
}): boolean {
  return e.source_rating != null && !(e.source_rating_count != null && e.source_rating_count > 0);
}

export function countWord(n:number,forms:readonly [string,string,string]):string {
 const lastTwo=Math.abs(n)%100,last=Math.abs(n)%10;
 return forms[lastTwo>=11&&lastTwo<=14?2:last===1?0:last>=2&&last<=4?1:2];
}
