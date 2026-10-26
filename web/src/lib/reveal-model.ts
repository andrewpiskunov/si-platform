import type { RevealView } from './game-v2.ts';

/** Interpolate only presentation. Completion and buzzer permissions remain server-owned. */
export function revealProgress(
  reveal: RevealView | undefined,
  snapshotTime: number,
  serverNow: number,
  paused: boolean,
  reducedMotion = false
): number {
  if (!reveal || reveal.duration_ms <= 0) return 1;
  const elapsed = reveal.elapsed_ms + (paused ? 0 : Math.max(0, serverNow - snapshotTime));
  const progress = Math.max(0, Math.min(1, elapsed / reveal.duration_ms));
  return reducedMotion ? Math.floor(progress * 4) / 4 : progress;
}

const graphemes = new Intl.Segmenter(undefined, { granularity: 'grapheme' });
export function revealText(text: string, reveal: RevealView | undefined, progress: number) {
  if (!reveal || reveal.kind !== 'text' || progress >= 1) return { visible: text, concealed: '' };
  // Wire offsets count Unicode codepoints, rather than JavaScript UTF-16 code units.
  const points = Array.from(text);
  const offset = Math.max(0, Math.min(points.length, reveal.text_offset ?? 0));
  const prefix = points.slice(0, offset).join('');
  const current = Array.from(graphemes.segment(points.slice(offset).join('')), (s) => s.segment);
  const count = Math.floor(current.length * Math.max(0, progress));
  return {
    visible: prefix + current.slice(0, count).join(''),
    concealed: current.slice(count).join('')
  };
}

export function imageRevealProgress(
  reveal: RevealView | undefined,
  progressive: boolean,
  stage: string,
  snapshotTime: number,
  serverNow: number,
  paused: boolean,
  reducedMotion = false
): number {
  // A shared media start can precede delivery of the present_question snapshot.
  // Do not display the whole image while waiting for its authoritative reveal.
  if (progressive && ['prepare_content', 'scheduled_content'].includes(stage)) return 0;
  return reveal?.kind === 'image'
    ? revealProgress(reveal, snapshotTime, serverNow, paused, reducedMotion)
    : 1;
}
