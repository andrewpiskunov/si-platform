import type { Participant } from './room-client.ts';
import type { SessionView } from './game-v2.ts';
export function removalTargets(
  participants: Participant[],
  you: string,
  view: SessionView
): Participant[] {
  if (!participants.some((p) => p.id === you && p.role === 'host' && !p.removed)) return [];
  const allowed = view.allowed_actions?.find((a) => a.type === 'remove_participant')?.targets || [];
  return participants.filter(
    (p) => !p.removed && p.role !== 'host' && p.id !== you && allowed.includes(p.id)
  );
}
