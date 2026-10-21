const $ = s => document.querySelector(s);
const params = new URLSearchParams(location.search);
let role = ['host','player','screen'].includes(params.get('role')) ? params.get('role') : 'host';
let state = { stage:'board', mode:'oral', count:3, score:0, blocked:0, paused:false, correct:null, price:100, played:[] };
const instance = crypto.randomUUID();
let introTimer;
let lastScene = '';
const defaultRules = {mode:'oral',penalties:true,falseStart:true,blockSeconds:3,introMS:2000};
state.rules={...defaultRules}; state.activeRules={...defaultRules};
const channel = new BroadcastChannel('efir-interaction-prototype');
const names = ['Анна','Михаил','Елена'];
const avatars = ['anna','mikhail','elena'];
let preferences={sound:false,volume:50,text:'normal',motion:false};
try{const saved=JSON.parse(sessionStorage.getItem('efir-prototype-preferences')||'null');if(saved)preferences={sound:saved.sound===true,volume:Number.isFinite(saved.volume)?Math.max(0,Math.min(100,saved.volume)):50,text:saved.text==='large'?'large':'normal',motion:saved.motion===true};}catch{}
let audioContext;
const question = 'Какой океан самый большой по площади?';
let cell = '';
function publish(){ channel.postMessage({type:'state',state}); }
channel.onmessage = ({data}) => {
  if(data.type === 'request' && role === 'host') publish();
  if(data.type === 'state'){ state = data.state; render(); }
};
channel.postMessage({type:'request'});
function update(change){ Object.assign(state,change); render(); publish(); }
function button(label, action, cls='primary') { const b=document.createElement('button'); b.textContent=label; b.className=cls; b.disabled=state.paused; b.onclick=action; return b; }
function showQuestion(id,price,topic){cell=id; const rules={...state.rules}; update({stage:'intro',topic,price,mode:rules.mode,activeRules:rules,correct:null,blocked:0,introOwner:instance,introEnd:Date.now()+rules.introMS,introRemaining:rules.introMS});}
function scheduleIntro(){
 clearTimeout(introTimer);
 if(state.stage==='intro'&&!state.paused&&state.introOwner===instance){
  introTimer=setTimeout(()=>{if(state.stage==='intro'&&!state.paused)update({stage:'reading'});},Math.max(0,state.introEnd-Date.now()));
 }
}
function buzz(){
 if(state.paused || state.blocked>Date.now()) return;
 if(state.stage==='reading'&&state.activeRules.falseStart) update({blocked:Date.now()+state.activeRules.blockSeconds*1000});
 else if(state.stage==='open') update({stage:'answering'});
}
function judge(correct){ if(state.stage!=='answering')return; update({stage:'result',correct,score:state.score+(correct?state.price:state.activeRules.penalties?-state.price:0)}); }
function next(){ update({stage:'board',blocked:0,correct:null,played:[...state.played,cell]}); }
function render(){
 const wasOpen=lastScene==='question'&&$('.stage').classList.contains('open');
 if(state.stage==='open'&&!state.paused&&!wasOpen&&preferences.sound)playTone();
 scheduleIntro();
 document.body.dataset.role=role;
 document.querySelectorAll('[data-role]').forEach(el=>{if(el.tagName==='BUTTON')el.setAttribute('aria-pressed',String(el.dataset.role===role));});
 $('#count').value=String(state.count); $('#count').disabled=role==='screen';
 $('#mode').value=state.mode; $('#mode').disabled=role==='screen'; $('#reset').hidden=role==='screen';
 $('#pause').textContent=state.paused?'Продолжить':'Пауза';
 const titles={board:'Выбирает Анна',intro:'Тема вопроса',reading:'Вопрос читается',open:'Приём нажатий открыт',answering:'Отвечает Анна',result:state.correct?'Ответ засчитан':'Ответ не засчитан'};
 $('#status').textContent=state.paused?'Игра на паузе':titles[state.stage];
 $('#price').textContent=state.stage==='board'?'4 темы · 16 вопросов':`${state.topic || "На карте"} · ${state.price}`;
 $('.stage').classList.toggle('open',state.stage==='open'&&!state.paused);
 const sceneKey=state.stage==='intro'?`intro:${state.topic}:${state.price}`:state.stage==='result'?'result':state.stage==='board'?'board':'question';
 const changed=lastScene!==sceneKey; lastScene=sceneKey;
 $('#scene').classList.toggle('scene-enter',changed&&sceneKey!=='board');
 $('#scene').replaceChildren();
 if(state.stage==='board'){
  const board=document.createElement('div');board.className='board';
  ['На карте','Наука вокруг','Книжная полка','Всякое разное'].forEach((topic,t)=>{
   const row=document.createElement('div');row.className='board-row';const label=document.createElement('div');label.className='topic';label.textContent=topic;row.append(label);
   [100,200,300,400].forEach(price=>{const id=`${t}-${price}`;const b=button(state.played.includes(id)?'—':String(price),()=>showQuestion(id,price,topic),'price');b.disabled=state.paused||role!=='host'||state.played.includes(id);b.setAttribute('aria-label',`${topic}, ${price}`);row.append(b);});board.append(row);
  });$('#scene').append(board);
 }else if(state.stage==='intro'){
  const card=document.createElement('div');card.className='topic-announcement';card.setAttribute('role','status');
  const label=document.createElement('p');label.className='announcement-label';label.textContent='ТЕМА';
  const heading=document.createElement('h2');heading.textContent=state.topic;
  const value=document.createElement('p');value.className='announcement-price';value.textContent=`${state.price} очков`;
  card.append(label,heading,value);$('#scene').append(card);
 }else{const h=document.createElement('h2');h.textContent=state.stage==='result'?'Тихий океан':question;$('#scene').append(h);}
 $('#signal').textContent=state.paused?'Пауза':({board:'Выберите стоимость вопроса',intro:'Сейчас появится вопрос',reading:state.activeRules.falseStart?'Ждите сигнала · раннее нажатие — фальстарт':'Ждите сигнала · кнопка пока закрыта',open:'Можно отвечать',answering:'Право ответа у Анны',result:state.correct?`Анна +${state.price}`:state.activeRules.penalties?`Анна −${state.price}`:'Анна · без изменения счёта'})[state.stage];
 const panel=$('#console');panel.replaceChildren();
 const title=document.createElement('h2');title.textContent='Пульт ведущего';panel.append(title);
 const hint=document.createElement('p');hint.className='muted';hint.textContent=state.mode==='oral'?'Устная игра · оценивает ведущий':'Ответ проверяет система';panel.append(hint);
 if(state.stage==='board'){const p=document.createElement('p');p.textContent='Анна выбирает тему и стоимость. Нажмите нужную ячейку табло.';panel.append(p);}
 else if(state.stage==='intro'){const p=document.createElement('p');p.textContent='Объявляем тему и стоимость. Вопрос появится автоматически.';panel.append(p);}
 else if(state.stage!=='result'){
  const answer=document.createElement('div');answer.className='answer';answer.innerHTML='<span class="muted">Видно только в представлении ведущего</span><strong>Тихий океан</strong><span class="muted">Вариант: «Тихий»</span>';panel.append(answer);
  if(state.stage==='reading')panel.append(button('Открыть кнопку',()=>update({stage:'open'})));
  if(state.stage==='open'){const p=document.createElement('p');p.textContent='Ждём нажатия игрока.';panel.append(p);panel.append(button('Анна нажимает · демо',buzz,'quiet'));}
  if(state.stage==='answering'){
   const p=document.createElement('p');p.textContent=state.mode==='oral'?'Анна отвечает голосом. Оцените ответ.':'Анна вводит ответ на своём экране. Ручная оценка не требуется.';panel.append(p);
   if(state.mode==='oral'){const d=document.createElement('div');d.className='decision';d.append(button('Верно',()=>judge(true)),button('Неверно',()=>judge(false),'quiet'));panel.append(d);}
  }
 }else{const p=document.createElement('p');p.textContent='Ответ раскрыт на общем экране. Можно вернуться к выбору.';panel.append(p);panel.append(button('Вернуться к табло',next));}
 if(role!=='host')panel.replaceChildren();
 $('#players').replaceChildren();Array.from({length:state.count},(_,i)=>names[i]||`Игрок ${i+1}`).forEach((name,i)=>{const p=document.createElement('div');p.className='person'+(i===0&&['answering','open'].includes(state.stage)?' active':'');p.innerHTML=`<img src="/avatars/${avatars[i%avatars.length]}.svg" alt=""><div><strong>${name}${role==='player'&&i===0?' · вы':''}</strong><small>${i===0?(state.stage==='answering'?'Отвечает':state.stage==='board'?'Выбирает':'Игрок'):'Игрок'}</small></div><span class="score">${i===0?state.score:i===1?200:0}</span>`;$('#players').append(p);});
 const controls=$('#player-controls');controls.replaceChildren();
 if(role==='player'){
  const b=button('Ответить',buzz);b.id='buzzer';controls.append(b);updateBuzzer();
  if(state.stage==='answering'&&state.mode==='auto'){
   b.hidden=true;const form=document.createElement('form');form.className='answer-form';form.innerHTML='<input aria-label="Ваш ответ" placeholder="Введите ответ" maxlength="120" autocomplete="off" required><button class="primary">Отправить ответ</button>';form.querySelector('input').disabled=state.paused;form.querySelector('button').disabled=state.paused;
   form.onsubmit=e=>{e.preventDefault();if(!state.paused)judge(['тихий','тихий океан'].includes(form.querySelector('input').value.trim().toLowerCase()));};controls.append(form);
  }
 }
 $('#guide').textContent=role==='screen'?'Демонстрационный общий экран · синхронизируется с вкладкой ведущего':role==='host'?'Прототип: все ячейки открывают один демонстрационный вопрос. Для проверки фальстарта переключитесь на «Игрок» до открытия кнопки.':'Прототип: переключайтесь на «Ведущий», чтобы открыть кнопку или оценить устный ответ. Пробел — нажатие кнопки.';
}
function updateBuzzer(){const b=$('#buzzer');if(!b)return;const remaining=Math.max(0,state.blocked-Date.now());b.disabled=state.paused||remaining>0||!['reading','open'].includes(state.stage)||(state.stage==='reading'&&!state.activeRules.falseStart);b.textContent=state.paused?'Пауза':remaining>0?`Фальстарт · ${(remaining/1000).toFixed(1)} с`:({board:'Ждём выбора вопроса',intro:'Объявление темы',reading:'Ждите сигнала',open:'Ответить · пробел',answering:'Вы отвечаете',result:'Ответ раскрыт'})[state.stage];}
setInterval(updateBuzzer,100);
document.querySelectorAll('button[data-role]').forEach(b=>b.onclick=()=>{role=b.dataset.role;render();});
$('#count').onchange=e=>update({count:Number(e.target.value)});
$('#mode').onchange=e=>update({mode:e.target.value,rules:{...state.rules,mode:e.target.value},stage:'board',blocked:0,correct:null});
$('#reset').onclick=()=>{cell='';update({stage:'board',score:0,blocked:0,paused:false,correct:null,played:[]});};
$('#pause').onclick=()=>{
 const timing=state.stage==='intro'?(state.paused?{introEnd:Date.now()+state.introRemaining}:{introRemaining:Math.max(0,state.introEnd-Date.now())}):{};
 update({...timing,paused:!state.paused});
};
$('#settings').onclick=openRules;
$('#display').onclick=()=>window.open('./index.html?role=screen','efir-demo-display');
document.addEventListener('keydown',e=>{if(e.code==='Space'&&role==='player'&&!['INPUT','SELECT','TEXTAREA','BUTTON'].includes(document.activeElement.tagName)&&!document.querySelector('dialog[open]')){e.preventDefault();if(!e.repeat)buzz();}});
function openRules(){
 const rules=state.rules;
 $('#rule-mode').value=rules.mode;$('#rule-penalties').checked=rules.penalties;
 $('#rule-false-start').checked=rules.falseStart;$('#rule-block').value=String(rules.blockSeconds);$('#rule-intro').value=String(rules.introMS);
 const allowed=role==='host'&&(state.stage==='board'||state.paused);
 $('#rules-fields').disabled=!allowed;$('#save-rules').disabled=!allowed;
 $('#rule-block').disabled=!rules.falseStart;
 $('#rules-notice').textContent=state.stage==='board'?'Изменения применятся к следующему выбранному вопросу.':state.paused?'Изменения вступят в силу со следующего вопроса. Текущий вопрос продолжится по прежним правилам.':'Чтобы изменить правила, поставьте игру на паузу. Сейчас доступен только просмотр.';
 $('#rules').showModal();
}
$('#rule-false-start').onchange=()=>$('#rule-block').disabled=!$('#rule-false-start').checked;
$('#rules-form').onsubmit=e=>{
 e.preventDefault();if(role!=='host'||(state.stage!=='board'&&!state.paused))return;
 const rules={mode:$('#rule-mode').value,penalties:$('#rule-penalties').checked,falseStart:$('#rule-false-start').checked,blockSeconds:Number($('#rule-block').value),introMS:Number($('#rule-intro').value)};
 update({rules,...(state.stage==='board'?{mode:rules.mode}:{})});$('#rules').close();
 $('#settings-toast').textContent='Правила сохранены · применятся со следующего вопроса';$('#settings-toast').hidden=false;
 clearTimeout(window.settingsToastTimer);window.settingsToastTimer=setTimeout(()=>$('#settings-toast').hidden=true,4000);
};
function applyPreferences(){
 document.body.classList.toggle('large-question',preferences.text==='large');document.body.classList.toggle('reduce-motion',preferences.motion);
 $('#pref-sound').checked=preferences.sound;$('#pref-volume').value=String(preferences.volume);$('#volume-value').textContent=`${preferences.volume}%`;$('#pref-text').value=preferences.text;$('#pref-motion').checked=preferences.motion;
 try{sessionStorage.setItem('efir-prototype-preferences',JSON.stringify(preferences));}catch{}
}
async function playTone(){
 try{
  audioContext ||= new AudioContext();await audioContext.resume();
  const osc=audioContext.createOscillator(),gain=audioContext.createGain(),now=audioContext.currentTime;
  osc.frequency.value=660;gain.gain.setValueAtTime(0,now);gain.gain.linearRampToValueAtTime(preferences.volume/100*.15,now+.015);gain.gain.exponentialRampToValueAtTime(.001,now+.22);
  osc.connect(gain);gain.connect(audioContext.destination);osc.start(now);osc.stop(now+.24);osc.onended=()=>{osc.disconnect();gain.disconnect();};
  $('#sound-status').textContent=preferences.volume?'Звук воспроизведён':'Громкость установлена на ноль';
 }catch{$('#sound-status').textContent='Не удалось воспроизвести звук в этом браузере';}
}
$('#personal').onclick=()=>{$('#sound-status').textContent='';$('#preferences').showModal();};
$('#pref-sound').onchange=e=>{preferences.sound=e.target.checked;applyPreferences();};
$('#pref-volume').oninput=e=>{preferences.volume=Number(e.target.value);applyPreferences();};
$('#pref-text').onchange=e=>{preferences.text=e.target.value;applyPreferences();};
$('#pref-motion').onchange=e=>{preferences.motion=e.target.checked;applyPreferences();};
$('#test-sound').onclick=playTone;
document.querySelectorAll('[data-close]').forEach(b=>b.onclick=()=>document.getElementById(b.dataset.close).close());
applyPreferences();
render();
