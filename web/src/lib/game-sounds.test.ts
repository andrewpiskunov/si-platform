import {test} from 'node:test';
import assert from 'node:assert/strict';
import {eventSound,type SoundSnapshot} from './game-sounds.ts';
const base:SoundSnapshot={seq:1,phase:'running',stage:'select_question',stageID:'s1',instance:'',type:'',roundType:'standard',decisionID:'',correct:false};
test('sound never replays an initial snapshot or duplicate update',()=>{
 assert.equal(eventSound(null,base),null);
 assert.equal(eventSound(base,{...base}),null);
 assert.equal(eventSound(base,{...base,seq:2}),null);
});
test('selection sounds once even when the price scene becomes the question',()=>{
 const price={...base,seq:2,stage:'question_price',stageID:'s2',instance:'q1',type:'withButton'};
 assert.equal(eventSound(base,price),'select');
 assert.equal(eventSound(price,{...price,seq:3,stage:'present_question',stageID:'s3'}),null);
});
test('pause and resumed snapshots do not announce old events',()=>{
 const paused={...base,seq:2,phase:'paused'};
 assert.equal(eventSound(base,paused),null);
 assert.equal(eventSound(paused,{...base,seq:3}),null);
});
test('buzzer, judgement and final use distinct sounds',()=>{
 const answer={...base,seq:2,stage:'collect_answer',stageID:'s2',type:'withButton',instance:'q1'};
 assert.equal(eventSound(base,answer),'buzz');
 assert.equal(eventSound(answer,{...answer,seq:3,stage:'resolved',decisionID:'d1',correct:true}),'correct');
 assert.equal(eventSound(answer,{...answer,seq:3,stage:'resolved',decisionID:'d1',correct:false}),'wrong');
 assert.equal(eventSound(base,{...base,seq:4,stage:'round_intro',stageID:'s4',roundType:'final'}),'final');
});
