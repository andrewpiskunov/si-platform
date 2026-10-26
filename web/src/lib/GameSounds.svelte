<script lang="ts">
  import {onMount,untrack} from 'svelte';
  import {eventSound,GameTonePlayer,type SoundSnapshot} from './game-sounds.ts';
  import type {SessionView} from './game-v2.ts';
  let {view,seq,phase,ready}: {view:SessionView;seq:number;phase:string;ready:boolean}=$props();
  let enabled=$state(false),volume=$state(40),busy=$state(false),message=$state(''),mounted=$state(false);
  const player=new GameTonePlayer();
  let previous:SoundSnapshot|null=null,alive=true;
  onMount(()=>{
    try {const stored=localStorage.getItem('si-game-volume');if(stored!==null&&Number.isFinite(Number(stored)))volume=Math.max(0,Math.min(100,Number(stored)));}catch{}
    mounted=true;
    return ()=>{alive=false;player.close();};
  });
  $effect(()=>{
    player.volume(enabled?volume/100:0);
    if(mounted)try{localStorage.setItem('si-game-volume',String(volume));}catch{}
  });
  $effect(()=>{
    const decision=view.decisions?.filter(d=>d.instance_id===view.question?.instance_id).at(-1);
    const snapshot:SoundSnapshot={seq,phase,stage:view.stage.kind,stageID:view.stage.id,instance:view.question?.instance_id||'',type:view.question?.type||'',roundType:view.round?.type||'',decisionID:decision?.id||'',correct:decision?.correct||false};
    const connected=ready;
    untrack(()=>{
      const sound=connected?eventSound(previous,snapshot):null;
      previous=connected?snapshot:null;
      if(sound&&enabled)player.play(sound);
    });
  });
  async function toggle() {
    if(enabled){enabled=false;message='Звуки выключены.';return;}
    busy=true;
    try{await player.enable(volume/100);if(alive){enabled=true;player.play('select');message='Звуки включены в этом окне.';}}
    catch{if(alive)message='Не удалось включить звук. Проверьте разрешения браузера.';}
    finally{if(alive)busy=false;}
  }
</script>
<details class="sound-settings">
  <summary>Звук: {enabled?'вкл':'выкл'}</summary>
  <div class="sound-menu">
    <button type="button" disabled={busy} aria-pressed={enabled} onclick={toggle}>{enabled?'Выключить звуки':'Включить звуки'}</button>
    <label>Громкость · {volume}%<input aria-label="Громкость" type="range" min="0" max="100" step="5" bind:value={volume} /></label>
    <button type="button" disabled={!enabled} onclick={()=>{player.play('correct');message='Проверка звука.';}}>Проверить звук</button>
    <p class="helper">Включается отдельно в каждом окне. Для телевизора включите звук на втором экране.</p>
    {#if message}<p class="helper" role="status">{message}</p>{/if}
  </div>
</details>
<style>
  .sound-settings{position:relative;padding:0;border:0;background:none;}
  summary{cursor:pointer;font-size:12px;padding:8px;list-style:none;border:1px solid var(--line);white-space:nowrap;}
  .sound-menu{position:absolute;right:0;top:100%;z-index:20;width:260px;max-width:85vw;display:grid;gap:12px;background:var(--surface);border:1px solid var(--line);padding:16px;box-shadow:0 8px 30px #0005;}
  label{font-size:13px;}input{width:100%;}p{margin:0;}
</style>
