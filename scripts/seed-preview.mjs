// Local preview only. Keep the demo account; tests intentionally remove their own data.
import { request } from '../e2e/node_modules/playwright/index.mjs';
import { randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
const base = process.env.BASE_URL || 'http://localhost:5173';
if (!['localhost', '127.0.0.1'].includes(new URL(base).hostname)) throw Error('Local preview only');
const api = await request.newContext({ baseURL: base });
async function call(method, path, data) {
 const r = await api.fetch('/api' + path, { method, data });
 if (!r.ok()) throw Error(`${path}: ${r.status()} ${await r.text()}`);
 return r.status() === 204 ? null : r.json();
}
const name = 'Разминка перед игрой';
const catalogue = await call('GET', '/catalogue?q=' + encodeURIComponent(name));
if (catalogue.items.some(p => p.name === name || p.title === name)) { console.log('Demo already published'); await api.dispose(); process.exit(0); }
const username = 'preview_' + randomBytes(6).toString('hex');
await call('POST', '/account/register', { username, password: randomBytes(24).toString('hex'), display_name: 'Редакция · демопакет' });
const topics = [
 ['На карте', [['Какой океан самый большой по площади?', 'Тихий'], ['В каком городе находится Колизей?', 'Рим'], ['Как называется столица Японии?', 'Токио'], ['На каком материке находится Перу?', 'Южная Америка'], ['Какой пролив разделяет Европу и Африку?', 'Гибралтарский']]],
 ['Наука вокруг', [['Какую планету называют красной?', 'Марс'], ['Сколько сторон у шестиугольника?', 'Шесть'], ['Какой химический элемент обозначают символом O?', 'Кислород'], ['В каких единицах измеряют электрическое сопротивление?', 'Омы'], ['Как называется переход вещества из твёрдого состояния сразу в газообразное?', 'Сублимация']]],
 ['Книжная полка', [['Кто написал «Евгения Онегина»?', 'Александр Пушкин'], ['Как зовут сыщика, живущего на Бейкер-стрит?', 'Шерлок Холмс'], ['Какой писатель создал Муми-троллей?', 'Туве Янссон'], ['Кто написал роман «Двадцать тысяч льё под водой»?', 'Жюль Верн'], ['Как называется вымышленный город в романе «Сто лет одиночества»?', 'Макондо']]],
 ['Всякое разное', [['Сколько клеток на стандартной шахматной доске?', '64'], ['Как называется японское искусство складывания бумаги?', 'Оригами'], ['Сколько струн у обычной скрипки?', 'Четыре'], ['Какой цвет получится при смешивании синей и жёлтой краски?', 'Зелёный'], ['Как называется знак &?', 'Амперсанд']]]
];
const document = { id: 'preview-warmup', version: '5.0', name, language: 'ru', restriction: '6+', difficulty: 2, tags: ['демо', 'для новичков'], info: { authors: ['Редакция · демопакет'] }, rounds: [{ id: 'r1', name: 'Разминка', type: 'standard', info: {}, themes: topics.map(([name, questions], t) => ({ id: `t${t}`, name, info: {}, questions: questions.map(([question, answer], q) => ({ id: `t${t}q${q}`, price: (q+1)*100, type: 'withButton', info: {}, right: [answer], params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: question }] }, { name: 'answer', type: 'content', items: [{ type: 'text', value: answer }] }] })) })) }] };
const draft = await call('POST', '/drafts', { document });
await call('POST', `/drafts/${draft.card.id}/publish`, { revision: 1, rights: { license: 'CC0-1.0', source_url: 'original', attribution: 'Демонстрационные вопросы проекта', confirmed: true } });
try {
 execFileSync('go', ['run', './cmd/admin', 'role', username, 'moderator'], { stdio: 'pipe' });
 await call('POST', `/moderation/${draft.card.id}`, { revision: 1, decision: 'approve', reason: 'Авторский тестовый пакет для локального просмотра' });
} finally { execFileSync('go', ['run', './cmd/admin', 'role', username, 'author'], { stdio: 'pipe' }); }
console.log(JSON.stringify({ pack_id: draft.card.id, name, questions: 20 }));
await api.dispose();
