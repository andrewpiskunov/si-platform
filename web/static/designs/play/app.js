const $ = s => document.querySelector(s);
const params = new URLSearchParams(location.search);
let role = ['host','player','screen'].includes(params.get('role')) ? params.get('role') : 'host';
let state = { stage:'board', mode:'oral', count:3, score:0, blocked:0, paused:false, correct:null, price:100, played:[] };
const channel = new BroadcastChannel('efir-interaction-prototype');
const names = ['Анна','Михаил','Елена'];
const avatars = ['anna','mikhail','elena'];
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
function showQuestion(id,price){cell=id; update({stage:'reading',price,correct:null,blocked:0});}
function buzz(){
 if(state.paused || state.blocked>Date.now()) return;
 if(state.stage==='reading') update({blocked:Date.now()+3000});
 else if(state.stage==='open') update({stage:'answering'});
}
function judge(correct){ if(state.stage!=='answering')return; update({stage:'result',correct,score:state.score+(correct?state.price:-state.price)}); }
function next(){ update({stage:'board',blocked:0,correct:null,played:[...state.played,cell]}); }
function render(){
 document.body.dataset.role=role;
 document.querySelectorAll('[data-role]').forEach(el=>{if(el.tagName==='BUTTON')el.setAttribute('aria-pressed',String(el.dataset.role===role));});
 $('#count').value=String(state.count); $('#count').disabled=role==='screen';
 $('#mode').value=state.mode; $('#mode').disabled=role==='screen'; $('#reset').hidden=role==='screen';
 $('#pause').textContent=state.paused?'Продолжить':'Пауза';
 const titles={board:'Выбирает Анна',reading:'Вопрос читается',open:'Приём нажатий открыт',answering:'Отвечает Анна',result:state.correct?'Ответ засчитан':'Ответ не засчитан'};
 $('#status').textContent=state.paused?'Игра на паузе':titles[state.stage];
 $('#price').textContent=state.stage==='board'?'4 темы · 16 вопросов':`На карте · ${state.price}`;
 $('.stage').classList.toggle('open',state.stage==='open'&&!state.paused);
 $('#scene').replaceChildren();
 if(state.stage==='board'){
  const board=document.createElement('div');board.className='board';
  ['На карте','Наука вокруг','Книжная полка','Всякое разное'].forEach((topic,t)=>{
   const row=document.createElement('div');row.className='board-row';const label=document.createElement('div');label.className='topic';label.textContent=topic;row.append(label);
   [100,200,300,400].forEach(price=>{const id=`${t}-${price}`;const b=button(state.played.includes(id)?'—':String(price),()=>showQuestion(id,price),'price');b.disabled=state.paused||role!=='host'||state.played.includes(id);b.setAttribute('aria-label',`${topic}, ${price}`);row.append(b);});board.append(row);
  });$('#scene').append(board);
 }else{const h=document.createElement('h2');h.textContent=state.stage==='result'?'Тихий океан':question;$('#scene').append(h);}
 $('#signal').textContent=state.paused?'Пауза':({board:'Выберите стоимость вопроса',reading:'Ждите сигнала · раннее нажатие — фальстарт',open:'Можно отвечать',answering:'Право ответа у Анны',result:state.correct?`Анна +${state.price}`:`Анна −${state.price}`})[state.stage];
 const panel=$('#console');panel.replaceChildren();
 const title=document.createElement('h2');title.textContent='Пульт ведущего';panel.append(title);
 const hint=document.createElement('p');hint.className='muted';hint.textContent=state.mode==='oral'?'Устная игра · оценивает ведущий':'Ответ проверяет система';panel.append(hint);
 if(state.stage==='board'){const p=document.createElement('p');p.textContent='Анна выбирает тему и стоимость. Нажмите нужную ячейку табло.';panel.append(p);}
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
function updateBuzzer(){const b=$('#buzzer');if(!b)return;const remaining=Math.max(0,state.blocked-Date.now());b.disabled=state.paused||remaining>0||!['reading','open'].includes(state.stage);b.textContent=state.paused?'Пауза':remaining>0?`Фальстарт · ${(remaining/1000).toFixed(1)} с`:({board:'Ждём выбора вопроса',reading:'Ждите сигнала',open:'Ответить · пробел',answering:'Вы отвечаете',result:'Ответ раскрыт'})[state.stage];}
setInterval(updateBuzzer,100);
document.querySelectorAll('button[data-role]').forEach(b=>b.onclick=()=>{role=b.dataset.role;render();});
$('#count').onchange=e=>update({count:Number(e.target.value)});
$('#mode').onchange=e=>update({mode:e.target.value,stage:'board',blocked:0,correct:null});
$('#reset').onclick=()=>{cell='';update({stage:'board',score:0,blocked:0,paused:false,correct:null,played:[]});};
$('#pause').onclick=()=>update({paused:!state.paused});
$('#settings').onclick=()=>$('#rules').showModal();
$('#display').onclick=()=>window.open('./index.html?role=screen','efir-demo-display');
document.addEventListener('keydown',e=>{if(e.code==='Space'&&role==='player'&&!['INPUT','SELECT','TEXTAREA','BUTTON'].includes(document.activeElement.tagName)&&!$('#rules').open){e.preventDefault();if(!e.repeat)buzz();}});
render();
