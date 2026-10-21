import { topics } from './questions.js';
const paths = {
  grid: '<rect x="3" y="3" width="18" height="18" rx="3"/><path d="M3 9h18M3 15h18M9 3v18M15 3v18"/>',
  books: '<path d="M4 4h4v16H4zM10 4h4v16h-4zM16 5l4-1 3 15-4 1z"/>',
  edit: '<path d="m15 5 4 4M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15z"/>',
  arrow: '<path d="M5 12h14m-6-6 6 6-6 6"/>',
  back: '<path d="M19 12H5m6-6-6 6 6 6"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
  reset: '<path d="M3 11a9 9 0 1 1 2 7M3 5v6h6"/>',
  play: '<path d="m9 5 11 7-11 7z"/>',
  check: '<path d="m5 12 4 4L19 6"/>',
  expand: '<path d="M9 3H3v6m12-6h6v6M3 15v6h6m12-6v6h-6"/>',
  eye: '<path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${paths[name]}</svg>`;
const design = document.body.dataset.design;
const titles = {broadcast: 'Эфир', club: 'Клуб', arena: 'Арена'};
const used = new Set(), edits = new Map();
let section = 'game', selection = [0,0], returnFocus, search = '';
const players = [['Анна','А','1 200'],['Михаил','М','800'],['Елена','Е','600'],['Саша','С','−200']];
document.querySelector('#app').innerHTML = `
  <div class="review-bar"><a href="index.html">${icon('back')} Все варианты</a><span>${titles[design]} <span class="review-divider">/</span> Интерактивный макет</span><a href="/">Открыть сервис ${icon('arrow')}</a></div>
  <header class="header"><a class="brand" href="${design}.html"><span class="brand-symbol" aria-hidden="true"><i></i><i></i><i></i><i></i></span><span>своя игра</span></a>
    <nav aria-label="Разделы"><button data-section="game">Игра</button><button data-section="catalogue">Пакеты</button><button data-section="editor">Мастерская</button></nav>
    <div class="header-end"><span class="room-label">Дружеская комната</span><span class="avatar">В</span></div>
  </header>
  <main id="main"></main>
  <footer class="footer"><span>Дизайн «${titles[design]}»</span><span>Вопросы настоящие. Участники и счёт — пример.</span><a href="../studio.html">Предыдущий макет</a></footer>
  <dialog aria-labelledby="dialog-title"><div class="question-top"><span id="dialog-topic"></span><button class="icon-button" id="close-question" aria-label="Закрыть вопрос">${icon('close')}</button></div><div class="question-value" id="dialog-value"></div><h2 id="dialog-title"></h2><p id="dialog-answer" hidden></p><div class="question-controls"><button class="button primary" id="answer">Показать ответ</button><button class="button subtle" id="return">К табло</button></div></dialog>`;
const main = document.querySelector('#main'), dialog = document.querySelector('dialog');
function playerList() {
  return `<div class="players-head"><h2>Игроки <span>4 / 12</span></h2><span class="sample">Пример</span></div><div class="players">${players.map(([name,letter,score],i)=>`<div class="player ${i===0?'current':''}"><div class="player-person"><span class="avatar player-avatar p${i}">${letter}</span><div><strong>${name}</strong><span>${i===0?'Выбирает вопрос':'В игре'}</span></div></div><div class="score">${score}</div></div>`).join('')}</div><div class="host"><span class="avatar host-avatar">В</span><div><strong>Вы — ведущий</strong><span>Открываете вопросы и ответы</span></div></div>`;
}
function openQuestion(t,q,preview=false) {
  returnFocus = document.activeElement;
  const pair = preview ? edits.get(`${t}:${q}`)||topics[t][1][q] : topics[t][1][q];
  document.querySelector('#dialog-topic').textContent=topics[t][0];
  document.querySelector('#dialog-value').textContent=(q+1)*100;
  document.querySelector('#dialog-title').textContent=pair[0];
  const answer=document.querySelector('#dialog-answer');answer.textContent=pair[1];answer.hidden=true;
  document.querySelector('#answer').hidden=false;
  document.querySelector('#return').textContent=preview?'К редактору':'К табло';
  document.querySelector('#answer').onclick=()=>{answer.hidden=false;document.querySelector('#answer').hidden=true;document.querySelector('#return').focus();if(!preview)used.add(`${t}:${q}`);};
  dialog.showModal();
}
function game() {
  main.innerHTML=`<div class="page-heading"><div><div class="room-context">Пятничная игра</div><h1>Разминка перед игрой</h1></div><button class="button secondary" id="focus-board">${icon('expand')}<span>Только табло</span></button></div>
    <div class="game-layout"><section class="game-stage" aria-label="Игровое поле"><div class="board-toolbar"><div class="round"><span class="round-num">1</span><div><strong>Первый раунд</strong><span>Выберите стоимость вопроса</span></div></div><div class="remaining"><b>${20-used.size}</b> вопросов осталось</div></div>
    <div class="board">${topics.map(([theme,qs],t)=>`<div class="topic"><span>${theme}</span></div>${qs.map((_,q)=>`<button data-t="${t}" data-q="${q}" ${used.has(`${t}:${q}`)?'class="played"':''} aria-label="${theme}, ${(q+1)*100}${used.has(`${t}:${q}`)?', просмотрен':''}">${used.has(`${t}:${q}`)?'<span class="played-mark" aria-hidden="true"></span>':(q+1)*100}</button>`).join('')}`).join('')}</div>
    <div class="game-caption"><span><span class="status-dot"></span>Анна выбирает вопрос</span><button class="text-button" id="reset">${icon('reset')}Начать заново</button></div></section><aside class="participants">${playerList()}</aside></div>
    <div class="instruction"><span class="instruction-icon">${icon('play')}</span><p>Попробуйте провести игру.<span>Откройте вопрос, покажите ответ и вернитесь к выбору.</span></p><span class="key-hint">Esc <span>закрыть вопрос</span></span></div>`;
  main.querySelectorAll('[data-q]').forEach(b=>b.onclick=()=>openQuestion(+b.dataset.t,+b.dataset.q));
  main.querySelector('#reset').onclick=()=>{used.clear();game();main.querySelector('#reset').focus();};
  main.querySelector('#focus-board').onclick=()=>{const focused=document.body.classList.toggle('board-focus');main.querySelector('#focus-board span').textContent=focused?'Весь интерфейс':'Только табло';main.querySelector('#focus-board').setAttribute('aria-pressed',String(focused));};
}
function catalogue() {
  main.innerHTML=`<div class="page-heading"><div><div class="room-context">Библиотека</div><h1>Пакеты для вашей компании</h1></div><button class="button primary" id="new-pack">${icon('edit')}В мастерскую</button></div>
  <div class="library-layout"><section class="featured"><div class="pack-cover"><span class="cover-brand">своя игра</span><h2>Разминка<br>перед игрой</h2><div class="cover-bottom"><span>На карте<br>Наука вокруг<br>Книжная полка<br>Всякое разное</span><strong>100<br>500</strong></div></div><div class="featured-caption"><span>Демонстрационный пакет</span><b>6+</b></div></section>
  <section class="pack-detail"><h2>Начните с хорошего вопроса</h2><p class="description">Знакомые темы, короткие вопросы и немного неожиданного. Пакет, чтобы освоиться с игрой и попробовать себя в роли ведущего.</p><dl class="pack-facts"><div><dt>Вопросов</dt><dd>20</dd></div><div><dt>Раундов</dt><dd>1</dd></div><div><dt>Сложность</dt><dd>Лёгкая</dd></div></dl><div class="pack-actions"><button class="button primary" id="play-pack">${icon('play')}Открыть табло</button><a class="button secondary" href="/">Настоящий каталог ${icon('arrow')}</a></div><div class="themes-title"><h3>Что внутри</h3><label class="search">${icon('search')}<input aria-label="Поиск тем" placeholder="Найти тему"></label></div><div id="themes"></div><p class="library-note">Пакет доступен и в работающем сервисе. Здесь вы просматриваете оформление.</p></section></div>`;
  main.querySelector('input').value=search;
  const render=()=>{const matches=topics.filter(([theme])=>theme.toLowerCase().includes(search.toLowerCase()));main.querySelector('#themes').innerHTML=matches.map(([theme])=>`<div class="theme-row"><span>${theme}</span><span>5 вопросов</span><strong>100–500</strong></div>`).join('')||'<p role="status" class="empty">Такой темы нет. Попробуйте «наука» или очистите поиск.</p>';};
  main.querySelector('input').oninput=e=>{search=e.target.value;render();};render();
  main.querySelector('#play-pack').onclick=()=>renderSection('game');main.querySelector('#new-pack').onclick=()=>renderSection('editor');
}
function save() {
  if(main.querySelector('#question'))edits.set(selection.join(':'),[main.querySelector('#question').value,main.querySelector('#right').value]);
}
function editor() {
  const [t,q]=selection,pair=edits.get(`${t}:${q}`)||topics[t][1][q];
  main.innerHTML=`<div class="page-heading"><div><div class="room-context">Мастерская</div><h1>Разминка перед игрой</h1></div><span class="draft-label">Локальный черновик</span></div><div class="editing-layout"><aside class="outline"><h2>Первый раунд</h2>${topics.map(([theme,qs],ti)=>`<div class="theme-outline"><h3>${theme}</h3><div>${qs.map((_,qi)=>`<button class="${t===ti&&q===qi?'chosen':''}" aria-pressed="${t===ti&&q===qi}" aria-label="${theme}, ${(qi+1)*100}" data-t="${ti}" data-q="${qi}">${(qi+1)*100}</button>`).join('')}</div></div>`).join('')}</aside><section class="form"><div class="form-heading"><h2>${topics[t][0]}</h2><span>${(q+1)*100} очков</span></div><label for="question">Текст вопроса</label><textarea id="question"></textarea><label for="right">Правильный ответ</label><input id="right"><p class="field-help">Во время игры ответ увидит только ведущий.</p><div class="form-actions"><button class="button primary" id="preview">${icon('eye')}Предпросмотр вопроса</button><span class="save-state" role="status">Правки сохраняются до перезагрузки страницы.</span></div></section></div>`;
  main.querySelector('#question').value=pair[0];main.querySelector('#right').value=pair[1];
  main.querySelectorAll('[data-q]').forEach(b=>b.onclick=()=>{save();selection=[+b.dataset.t,+b.dataset.q];editor();main.querySelector('.chosen').focus();});
  main.querySelectorAll('input,textarea').forEach(el=>el.oninput=()=>{save();main.querySelector('.save-state').textContent='Изменения в макете сохранены до перезагрузки.';});
  main.querySelector('#preview').onclick=()=>{save();if(!edits.get(selection.join(':')).every(s=>s.trim())){main.querySelector('.save-state').textContent='Заполните вопрос и правильный ответ.';return;}openQuestion(t,q,true);};
}
function renderSection(next) {if(section==='editor')save();section=next;document.body.classList.remove('board-focus');document.querySelectorAll('[data-section]').forEach(b=>b.setAttribute('aria-current',b.dataset.section===next?'page':'false'));({game,catalogue,editor})[next]();}
document.querySelectorAll('[data-section]').forEach(b=>b.onclick=()=>renderSection(b.dataset.section));
document.querySelector('#close-question').onclick=()=>dialog.close();document.querySelector('#return').onclick=()=>dialog.close();
dialog.addEventListener('close',()=>{if(section==='game'){const t=returnFocus?.dataset.t,q=returnFocus?.dataset.q;game();main.querySelector(`[data-t="${t}"][data-q="${q}"]`)?.focus();}else returnFocus?.focus();});
renderSection('game');
