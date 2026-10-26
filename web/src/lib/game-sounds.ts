export type GameSound='start'|'select'|'special'|'buzz'|'correct'|'wrong'|'final'|'finish';
export interface SoundSnapshot {seq:number;phase:string;stage:string;stageID:string;instance:string;type:string;roundType:string;decisionID:string;correct:boolean;}
export function eventSound(previous:SoundSnapshot|null, current:SoundSnapshot):GameSound|null {
  if(!previous||current.seq<=previous.seq||current.phase==='paused'||previous.phase==='paused') return null;
  if(current.phase==='finished'&&previous.phase!=='finished') return 'finish';
  if(current.decisionID&&current.decisionID!==previous.decisionID) return current.correct?'correct':'wrong';
  if(current.stage==='round_intro'&&current.stageID!==previous.stageID || previous.phase==='lobby'&&current.phase==='running') return current.roundType==='final'?'final':'start';
  if(current.stage==='question_type'&&current.stageID!==previous.stageID) return 'special';
  if(current.stage==='collect_answer'&&current.stageID!==previous.stageID&&['simple','withButton'].includes(current.type)) return 'buzz';
  if(current.instance&&current.instance!==previous.instance) return 'select';
  return null;
}

// Original synthesized cues: no downloaded recordings or SIGame assets.
const notes:Record<GameSound,number[]>={start:[392,523,659],select:[880],special:[220,330,440],buzz:[660,880],correct:[523,659,784],wrong:[220,165],final:[262,392,523,784],finish:[523,784,1046]};
export class GameTonePlayer {
  private context:AudioContext|null=null;
  private master:GainNode|null=null;
  async enable(volume:number) {
    if(!this.context) {this.context=new AudioContext();this.master=this.context.createGain();this.master.connect(this.context.destination);}
    this.volume(volume);
    await this.context.resume();
    if(this.context.state!=='running') throw new Error('sound_unavailable');
  }
  volume(value:number) {if(this.master) this.master.gain.value=Math.max(0,Math.min(1,value));}
  play(sound:GameSound) {
    const context=this.context,master=this.master;
    if(!context||!master||context.state!=='running') return;
    notes[sound].forEach((frequency,index)=>{
      const start=context.currentTime+index*.12,end=start+.18;
      const oscillator=context.createOscillator(),gain=context.createGain();
      oscillator.type='sine';oscillator.frequency.value=frequency;
      gain.gain.setValueAtTime(.001,start);gain.gain.exponentialRampToValueAtTime(.12,start+.015);gain.gain.exponentialRampToValueAtTime(.001,end);
      oscillator.connect(gain).connect(master);oscillator.start(start);oscillator.stop(end);
      oscillator.onended=()=>{oscillator.disconnect();gain.disconnect();};
    });
  }
  close() {const context=this.context;this.context=null;this.master=null;void context?.close().catch(()=>{});}
}
