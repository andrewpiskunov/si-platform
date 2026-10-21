const topics = [
  ['На карте', [['Какой океан самый большой по площади?', 'Тихий океан'], ['В каком городе находится Колизей?', 'Рим'], ['Как называется столица Японии?', 'Токио'], ['На каком материке находится Перу?', 'Южная Америка'], ['Какой пролив разделяет Европу и Африку?', 'Гибралтарский']]],
  ['Наука вокруг', [['Какую планету называют красной?', 'Марс'], ['Сколько сторон у шестиугольника?', 'Шесть'], ['Какой химический элемент обозначают символом O?', 'Кислород'], ['В каких единицах измеряют электрическое сопротивление?', 'Омы'], ['Как называется переход вещества из твёрдого состояния сразу в газообразное?', 'Сублимация']]],
  ['Книжная полка', [['Кто написал «Евгения Онегина»?', 'Александр Пушкин'], ['Как зовут сыщика, живущего на Бейкер-стрит?', 'Шерлок Холмс'], ['Какая писательница создала Муми-троллей?', 'Туве Янссон'], ['Кто написал роман «Двадцать тысяч льё под водой»?', 'Жюль Верн'], ['Как называется вымышленный город в романе «Сто лет одиночества»?', 'Макондо']]],
  ['Всякое разное', [['Сколько клеток на стандартной шахматной доске?', '64'], ['Как называется японское искусство складывания бумаги?', 'Оригами'], ['Сколько струн у обычной скрипки?', 'Четыре'], ['Какой цвет получится при смешивании синей и жёлтой краски?', 'Зелёный'], ['Как называется знак &?', 'Амперсанд']]]
];
const packs = [
  { title: 'Разминка перед игрой', cover: 'ВСЁ НАЧИНАЕТСЯ\nС СОТНИ.', desc: 'География, книги, наука. Двадцать вопросов для первой встречи за табло.', count: 20, level: 'Лёгкий', type: 'Для начала', label: 'ДЕМОПАКЕТ / 01', actual: true },
  { title: 'Кино между строк', cover: 'ТИШИНА.\nИДЁТ ВОПРОС.', desc: 'Знакомые кадры, неожиданные роли и истории по ту сторону экрана.', count: 30, level: 'Средний', type: 'Кино', label: 'ПРИМЕР ОФОРМЛЕНИЯ / 02' },
  { title: 'Дальше школьной карты', cover: 'МИР БОЛЬШЕ,\nЧЕМ КАЖЕТСЯ.', desc: 'Города, границы и места, о которых хочется узнать немного больше.', count: 25, level: 'Средний', type: 'География', label: 'ПРИМЕР ОФОРМЛЕНИЯ / 03' }
];
const main = document.querySelector('#main');
const dialog = document.querySelector('#question-dialog');
const used = new Set();
const drafts = new Map();
let currentScreen = 'game', filter = 'Все пакеты', search = '', selected = [0, 0], toastTimer;
function toast(message) {
  const node = document.querySelector('.toast'); node.textContent = message; node.hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => node.hidden = true, 6000);
}
function question(t, q, custom) {
  const pair = custom || topics[t][1][q];
  document.querySelector('#question-theme').textContent = topics[t][0];
  document.querySelector('#question-price').textContent = (q + 1) * 100;
  document.querySelector('#question-title').textContent = pair[0];
  const answer = document.querySelector('#question-answer'); answer.hidden = true; answer.textContent = pair[1];
  document.querySelector('#show-answer').onclick = () => { answer.hidden = false; if (!custom) used.add(`${t}:${q}`); };
  dialog.showModal();
}
function game() {
  main.innerHTML = `<div class="page-title"><div><div class="eyebrow">За столом сегодня</div><h1>Пятничная игра</h1><p>Разминка перед игрой · 4 игрока из 12</p></div><div class="title-actions"><button class="quiet" id="invite">Пригласить друзей ↗</button><button class="gold" id="room-settings">Настройки комнаты</button></div></div>
  <div class="roundbar"><div class="round-tabs"><b>01 &nbsp; Разминка</b><span>4 темы</span><span>20 вопросов</span></div><div class="turn"><span class="turn-dot"></span>Анна выбирает вопрос</div></div>
  <div class="game-board" aria-label="Игровое табло">${topics.map(([topic, questions],t)=>`<div class="topic">${topic}</div>${questions.map((_,q)=>`<button data-t="${t}" data-q="${q}" class="${used.has(`${t}:${q}`)?'used':''}" aria-label="${topic}, ${(q+1)*100}${used.has(`${t}:${q}`)?', просмотрен':''}">${used.has(`${t}:${q}`)?'·':(q+1)*100}</button>`).join('')}`).join('')}</div>
  <div class="under-board"><span>Нажмите на стоимость, чтобы открыть вопрос</span><b>${20-used.size} из 20 осталось</b></div>
  <div class="players-heading"><strong>Участники</strong><span>Ведущий: Алексей · вы</span></div>
  <div class="players">${[['Анна','А','1 200','выбирает'],['Михаил','М','800',''],['Елена','Е','600',''],['Саша','С','−200','']].map(([name,letter,score,status],i)=>`<div class="podium ${i===0?'active':''}"><div class="podium-top"><span><span class="initial">${letter}</span>${name}</span><small>${status}</small></div><div class="podium-score">${score}</div></div>`).join('')}</div>
  <div class="hostbar"><p><b>Вы — ведущий.</b> Откройте вопрос и покажите ответ.<br>В макете можно просмотреть все 20 вопросов демопакета.</p><button class="quiet" id="reset-board">Начать просмотр заново</button></div>`;
  main.querySelectorAll('[data-q]').forEach(b => b.onclick = () => question(Number(b.dataset.t), Number(b.dataset.q)));
  main.querySelector('#reset-board').onclick = () => { used.clear(); game(); toast('Все вопросы снова открыты для выбора.'); };
  main.querySelector('#invite').onclick = () => toast('Приглашения доступны в настоящем сервисе. Этот экран показывает дизайн комнаты.');
  main.querySelector('#room-settings').onclick = () => toast('Для сравнения оформления переключите «Классика / Вечер» в меню. Игровые настройки здесь не подключены.');
}
function catalogue() {
  main.innerHTML = `<div class="page-title"><div><div class="eyebrow">Библиотека сообщества</div><h1>Во что играем сегодня?</h1><p>Выберите пакет для своей компании.</p></div><button class="gold" id="create-pack">＋ Создать пакет</button></div>
  <div class="catalogue-tools"><label class="search"><span aria-hidden="true">⌕</span><input aria-label="Поиск пакетов" placeholder="Название или тема пакета"></label><button class="quiet" id="import-pack">Импорт .siq ↗</button></div>
  <div class="chips" aria-label="Темы пакетов">${['Все пакеты','Для начала','Кино','География'].map(f=>`<button data-filter="${f}" aria-pressed="${filter===f}" class="${filter===f?'selected':''}">${f}</button>`).join('')}</div>
  <div class="section-label"><strong>В вашей подборке</strong><span id="pack-count"></span></div><div class="catalogue-grid"></div>
  <p class="catalogue-note">«Разминка перед игрой» опубликована в настоящем каталоге. Два других пакета показывают оформление будущей библиотеки.</p>`;
  main.querySelector('input').value=search;
  main.querySelector('input').oninput=e=>{search=e.target.value; renderPacks();};
  main.querySelectorAll('[data-filter]').forEach(b=>b.onclick=()=>{filter=b.dataset.filter;catalogue();});
  main.querySelector('#create-pack').onclick=()=>screen('editor');
  main.querySelector('#import-pack').onclick=()=>toast('Импорт SIQ работает в основном сервисе. В этом макете показан только интерфейс.');
  renderPacks();
}
function renderPacks() {
  const results = packs.filter(p=>(filter==='Все пакеты'||p.type===filter)&&(p.title+' '+p.desc).toLowerCase().includes(search.toLowerCase()));
  main.querySelector('#pack-count').textContent=`Найдено: ${results.length}`;
  main.querySelector('.catalogue-grid').innerHTML=results.map(p=>`<article class="pack"><div class="pack-art"><small>${p.label}</small><span class="mini">${p.cover.replace('\n','<br>')}</span></div><div class="pack-body"><h2>${p.title}</h2><p>${p.desc}</p><div class="pack-meta"><span>${p.count} вопросов</span><span>${p.level} · 6+</span></div><button class="quiet" data-pack="${p.actual?'actual':'example'}">${p.actual?'Открыть игровое табло':'Посмотреть пример комнаты'} ↗</button></div></article>`).join('')||'<p class="empty">Ничего не найдено. Попробуйте другую тему.</p>';
  main.querySelectorAll('[data-pack]').forEach(b=>b.onclick=()=>{screen('game');if(b.dataset.pack!=='actual')toast('Для примера открыт демопакет «Разминка перед игрой».');});
}
function remember() {
  const text = main.querySelector('#question-text'), answer = main.querySelector('#answer-text');
  if(text && answer)drafts.set(selected.join(':'),[text.value,answer.value]);
}
function editor() {
  const [t,q]=selected, pair=drafts.get(selected.join(':'))||topics[t][1][q];
  main.innerHTML=`<div class="page-title"><div><div class="eyebrow">Мастерская / Черновик</div><h1>Разминка перед игрой</h1><p>1 раунд · 4 темы · 20 вопросов</p></div><button class="gold" id="validate">Проверить вопрос</button></div><div class="editor-layout"><aside class="tree"><div class="eyebrow">Раунд 01 / Разминка</div>${topics.map(([topic,questions],ti)=>`<div class="theme-group"><h3>${topic}</h3><div class="question-list">${questions.map((_,qi)=>`<button aria-label="${topic}, ${(qi+1)*100}" data-t="${ti}" data-q="${qi}" class="${ti===t&&qi===q?'selected':''}">${(qi+1)*100}</button>`).join('')}</div></div>`).join('')}</aside><section class="editor-sheet"><div class="eyebrow">${topics[t][0]} &nbsp; / &nbsp; ${(q+1)*100}</div><label for="question-text">Вопрос</label><textarea id="question-text"></textarea><label for="answer-text">Правильный ответ</label><input id="answer-text"><div class="answer-hint">Ответ виден только ведущему до завершения вопроса.</div><div class="editor-bottom"><span id="editor-state" role="status">Правки остаются в макете до перезагрузки страницы.</span><button class="quiet" id="preview-question">На игровом экране ↗</button></div></section></div>`;
  main.querySelector('#question-text').value=pair[0];main.querySelector('#answer-text').value=pair[1];
  main.querySelectorAll('[data-q]').forEach(b=>b.onclick=()=>{remember();selected=[Number(b.dataset.t),Number(b.dataset.q)];editor();});
  main.querySelectorAll('textarea,input').forEach(el=>el.oninput=remember);
  function validate(){remember();const pair=drafts.get(selected.join(':'));const ok=pair.every(s=>s.trim());main.querySelector('#editor-state').textContent=ok?'Вопрос и ответ заполнены. Изменения не опубликованы.':'Заполните текст вопроса и ответ.';return ok;}
  main.querySelector('#validate').onclick=validate;
  main.querySelector('#preview-question').onclick=()=>{if(validate())question(t,q,drafts.get(selected.join(':')));};
}
function screen(name){if(currentScreen==='editor')remember();currentScreen=name;document.querySelectorAll('[data-screen]').forEach(b=>b.setAttribute('aria-current',b.dataset.screen===name?'page':'false'));document.querySelector('#breadcrumb').textContent={game:'Игровая комната / Пятничная игра',catalogue:'Пакеты вопросов / Подборка',editor:'Мастерская / Разминка перед игрой'}[name];({game,catalogue,editor})[name]();}
document.querySelectorAll('[data-screen]').forEach(b=>b.onclick=()=>screen(b.dataset.screen));
document.querySelectorAll('[data-palette]').forEach(b=>b.onclick=()=>{document.body.classList.toggle('night',b.dataset.palette==='night');document.querySelectorAll('[data-palette]').forEach(el=>el.setAttribute('aria-pressed',String(el===b)));});
document.querySelectorAll('.close').forEach(b=>b.onclick=()=>dialog.close());
dialog.addEventListener('close',()=>{if(currentScreen==='game'){const focused=document.activeElement;const t=focused?.dataset.t,q=focused?.dataset.q;game();if(t!==undefined)main.querySelector(`[data-t="${t}"][data-q="${q}"]`)?.focus();}});
screen('game');
