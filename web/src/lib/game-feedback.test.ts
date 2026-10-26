import {test} from 'node:test';
import assert from 'node:assert/strict';
import {questionFeedback,playerDecision} from './game-feedback.ts';
import type {SessionView} from './game-v2.ts';
const decision={id:'d1',participant_id:'p1',correct:false,delta:-100,reason:'',question_id:'q1',instance_id:'instance-1'};
const make=(kind:string,decisions=[decision])=>({question:{instance_id:'instance-1'},stage:{kind},decisions} as SessionView);
test('feedback follows current question instance and never reuses a previous question score',()=>{
 const view=make('resolved');
 assert.equal(questionFeedback(view)?.decision?.id,'d1');
 view.question!.instance_id='instance-2';
 assert.equal(questionFeedback(view),null);
 assert.equal(playerDecision(view,'p1'),undefined);
});
test('the answering player takes precedence over the previous wrong answer',()=>{
 const view=make('collect_answer');view.stage.turn_id='p2';
 assert.deepEqual(questionFeedback(view),{key:'answer:p2',kind:'answering',participantID:'p2'});
});
test('zero penalty remains an explicit wrong answer and corrections use their actual delta',()=>{
 const view=make('resolved',[{...decision,delta:0}]);
 assert.equal(questionFeedback(view)?.kind,'incorrect');
 assert.equal(questionFeedback(view)?.decision?.delta,0);
 view.decisions!.push({...decision,id:'d2',delta:200,correct:true,supersedes:'d1'});
 assert.equal(questionFeedback(view)?.kind,'correction');
 assert.equal(playerDecision(view,'p1')?.id,'d2');
});
