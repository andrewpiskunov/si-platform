import type {Decision, SessionView} from './game-v2.ts';
export interface QuestionFeedback {
  key:string;
  kind:'answering'|'judging'|'correct'|'incorrect'|'correction';
  participantID:string;
  decision?:Decision;
}
export function playerDecision(view:SessionView, id:string):Decision|undefined {
  if(!view.question) return undefined;
  return view.decisions?.filter(d=>d.instance_id===view.question!.instance_id&&d.participant_id===id).at(-1);
}
export function questionFeedback(view:SessionView):QuestionFeedback|null {
  if(!view.question) return null;
  if(['collect_answer','judge'].includes(view.stage.kind)&&view.stage.turn_id) {
    return {key:'answer:'+view.stage.turn_id,kind:view.stage.kind==='judge'?'judging':'answering',participantID:view.stage.turn_id};
  }
  const decision=view.decisions?.filter(d=>d.instance_id===view.question!.instance_id).at(-1);
  return decision ? {key:decision.id,kind:decision.supersedes?'correction':decision.correct?'correct':'incorrect',participantID:decision.participant_id,decision} : null;
}
