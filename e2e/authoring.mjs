import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5173';
const output = process.env.ARTIFACT_DIR || '../.local/browser';
const root = fileURLToPath(new URL('..', import.meta.url));
const selectedCase = process.env.AUTHORING_CASE || 'all';
const run = promisify(execFile);
const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();
const errors = [];
const suffix = randomBytes(6).toString('hex');
const username = 'authoring_' + suffix;
const password = randomBytes(20).toString('hex');
const report = {};
let registered = false;
page.on('pageerror', error => errors.push(error.message));
await mkdir(output, { recursive: true });
async function api(method, path, data) {
  const response = await context.request.fetch(base + '/api' + path, { method, data });
  assert.ok(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`);
  return response.status() === 204 ? null : response.json();
}
async function visit(path) {
  const response = await page.goto(base + path);
  await page.waitForLoadState('networkidle');
  return response;
}
async function until(fn, message) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(message);
}
function document(name, restriction = '6+') {
  return {
    id: 'authoring-document', version: '5.0', name, language: 'ru', restriction,
    difficulty: 3, tags: ['test_' + suffix], info: { authors: ['Автор'] },
    rounds: [1, 2].map(r => ({
      id: `r${r}`, name: `Раунд ${r}`, type: 'standard', info: {},
      themes: [1, 2].map(t => ({
        id: `r${r}t${t}`, name: `Тема ${r}.${t}`, info: {},
        questions: [1, 2].map(q => ({
          id: `r${r}t${t}q${q}`, price: q * 100, type: 'withButton',
          info: { extension: `metadata-${r}-${t}-${q}`, sources: ['Авторский вопрос'] },
          right: [`Ответ ${r}.${t}.${q}`], params: ['question', 'answer'].map(group => ({
            name: group, type: 'content', items: [1, 2].map(n => ({
              type: 'text', value: `${group} ${r}.${t}.${q}.${n}`, duration_ms: n * 1000
            }))
          }))
        }))
      }))
    }))
  };
}
function shape(doc) {
  return doc.rounds.map(r => ({ name: r.name, themes: r.themes.map(t => ({
    name: t.name, questions: t.questions.map(q => ({ price: q.price, right: q.right,
      extension: q.info.extension, sources: q.info.sources,
      fragments: q.params.filter(p => ['question', 'answer'].includes(p.name)).map(p => ({
        name: p.name, items: p.items.map(i => ({ type: i.type, value: i.value, duration_ms: i.duration_ms }))
      }))
    }))
  })) }));
}
async function saved(id, expected) {
  await until(async () => {
    if (!(await page.getByRole('status').filter({ hasText: 'Все изменения сохранены' }).count())) return false;
    const current = await api('GET', `/drafts/${id}`);
    return JSON.stringify(shape(current.document)) === JSON.stringify(shape(expected));
  }, 'reordered document did not reach durable autosave');
}
const move = name => page.getByRole('button', { name, exact: true });
async function ageFilter(value) {
  const filter = page.getByLabel('Возрастная отметка', { exact: true });
  if (!(await filter.isVisible())) await page.getByText('Ещё фильтры', { exact: true }).click();
  await filter.selectOption(value);
}
try {
  await api('POST', '/account/register', { username, password, display_name: 'Автор проверки' });
  registered = true;
  if (selectedCase !== 'editor') {
    await visit('/');
    assert.equal(await page.getByLabel('Возрастная отметка', { exact: true }).count(), 1, 'age filter missing');
    assert.ok(process.env.DATABASE_URL, 'DATABASE_URL for the local preview database is required to provision a synthetic moderator');
    await run(process.env.GO || 'go', ['run', './cmd/admin', 'role', username, 'moderator'], { cwd: root, env: process.env, timeout: 60000 });
    const marker = 'Астрономия' + suffix;
    const ids = [];
    for (let i = 0; i < 53; i++) {
      const doc = document(`Каталог ${suffix} ${i}`, i === 51 ? '18+' : i === 52 ? '' : '6+');
      doc.rounds = [doc.rounds[0]];
      doc.rounds[0].themes = [doc.rounds[0].themes[0]];
      doc.rounds[0].themes[0].name = marker;
      const draft = await api('POST', '/drafts', { document: doc });
      ids.push(draft.card.id);
      await api('POST', `/drafts/${draft.card.id}/publish`, { revision: 1, rights: {
        license: 'Own', source_url: 'original', attribution: 'Автор проверки', confirmed: true
      } });
      await api('POST', `/moderation/${draft.card.id}`, { revision: 1, decision: 'approve', reason: '' });
    }
    const credentials = await api('POST', '/rooms', { pack_id: ids[0], host_name: 'Ведущий', theme: 'pirate', title: 'Контекст каталога' });
    await page.evaluate(c => sessionStorage.setItem(`room:${c.room_id}`, JSON.stringify(c)), credentials);
    await visit('/?room=' + credentials.room_id);
    await page.getByLabel('Поиск пакетов', { exact: true }).fill(marker.toUpperCase());
    await ageFilter('12');
    await page.getByLabel('Язык', { exact: true }).selectOption('ru');
    await page.getByRole('button', { name: 'Найти', exact: true }).click();
    await page.waitForURL(url => url.searchParams.get('age') === '12' && url.searchParams.get('q') === marker.toUpperCase());
    await until(() => page.locator('.pack-card').count().then(n => n === 50), 'published theme search/age did not render 50 results');
    assert.equal(new URL(page.url()).searchParams.get('age'), '12');
    assert.equal(new URL(page.url()).searchParams.get('room'), credentials.room_id);
    await until(() => page.locator('[data-theme="pirate"]').count(), 'room appearance lost on filtered catalogue');
    const next = page.getByRole('link', { name: 'Ещё пакеты →', exact: true });
    const nextURL = new URL(await next.getAttribute('href'), base);
    assert.equal(nextURL.searchParams.get('q'), marker.toUpperCase());
    assert.equal(nextURL.searchParams.get('age'), '12');
    assert.equal(nextURL.searchParams.get('language'), 'ru');
    assert.equal(nextURL.searchParams.get('room'), credentials.room_id);
    await next.click();
    await page.waitForURL(url => url.searchParams.get('offset') === '50');
    await until(() => page.locator('.pack-card').count().then(n => n === 1), 'second catalogue page incorrect');
    const ssr = await page.request.get(page.url());
    assert.ok((await ssr.text()).includes('Каталог ' + suffix), 'SSR omitted filtered card');
    await page.getByRole('link', { name: '← Назад', exact: true }).click();
    await page.waitForURL(url => url.searchParams.get('offset') === '0');
    await until(() => page.locator('.pack-card').count().then(n => n === 50), 'back pagination lost filters');
    await ageFilter('unrated');
    await page.getByRole('button', { name: 'Найти', exact: true }).click();
    await page.waitForURL(url => url.searchParams.get('age') === 'unrated');
    await until(() => page.locator('.pack-card').count().then(n => n === 1), 'unrated filter incorrect');
    assert.ok((await page.locator('.pack-card').innerText()).includes(`Каталог ${suffix} 52`));
    const invalid = await page.request.get(base + '/api/catalogue?age=12%2B');
    assert.equal(invalid.status(), 400);
    await page.setViewportSize({ width: 390, height: 844 });
    await ageFilter('unrated');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile catalogue overflow');
    await page.screenshot({ path: output + '/authoring-catalogue-mobile.png', fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });
    report.catalogue = { theme_search: true, age: true, unrated: true, invalid_age: true, pagination: true, ssr: true, room_context: true, mobile_overflow: false };
    console.log('[authoring] catalogue GREEN');
  }
  if (selectedCase !== 'catalogue') {
    const expected = document('Перестановка ' + suffix);
    const draft = await api('POST', '/drafts', { document: expected });
    const id = draft.card.id;
    await visit('/studio/' + id);
    assert.equal(await move('Раунд 1 «Раунд 1»: ниже').count(), 1, 'round reorder controls missing');
    assert.equal(await move('Раунд 1 «Раунд 1»: выше').isDisabled(), true);
    await move('Раунд 1 «Раунд 1»: ниже').focus();
    await page.keyboard.press('Enter');
    expected.rounds.reverse();
    assert.equal(await page.getByLabel('Текст вопроса, фрагмент 1', { exact: true }).inputValue(), 'question 1.1.1.1', 'selection changed with parent');
    await move('Тема 1 «Тема 1.1» (раунд 2): ниже').click();
    expected.rounds[1].themes.reverse();
    await move('Вопрос 1 за 100 в теме «Тема 1.1»: ниже').click();
    expected.rounds[1].themes[1].questions.reverse();
    await move('Фрагмент вопроса 1: ниже').click();
    expected.rounds[1].themes[1].questions[1].params[0].items.reverse();
    await page.getByText('Пояснение и источники', { exact: true }).click();
    await move('Фрагмент ответа 1: ниже').click();
    expected.rounds[1].themes[1].questions[1].params[1].items.reverse();
    await saved(id, expected);
    await page.getByRole('button', { name: /Отменить/ }).click();
    expected.rounds[1].themes[1].questions[1].params[1].items.reverse();
    await saved(id, expected);
    await move('Фрагмент ответа 1: ниже').click();
    expected.rounds[1].themes[1].questions[1].params[1].items.reverse();
    await saved(id, expected);
    const before = await api('GET', `/drafts/${id}`);
    assert.deepEqual(before.document.rounds.map(r => r.id), ['r2', 'r1']);
    assert.deepEqual(before.document.rounds[1].themes.map(t => t.id), ['r1t2', 'r1t1']);
    assert.deepEqual(before.document.rounds[1].themes[1].questions.map(q => q.id), ['r1t1q2', 'r1t1q1']);
    await page.reload(); await page.waitForLoadState('networkidle');
    assert.equal(await page.getByLabel('Текст вопроса, фрагмент 1', { exact: true }).inputValue(), 'question 1.1.1.2', 'reload lost selected question');
    const after = await api('GET', `/drafts/${id}`);
    assert.deepEqual(shape(after.document), shape(expected));
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile editor overflow');
    await page.screenshot({ path: output + '/authoring-editor-mobile.png', fullPage: true });
    const downloadEvent = page.waitForEvent('download');
    await page.getByRole('button', { name: '↓ SIQ', exact: true }).click();
    const download = await downloadEvent;
    const archivePath = output + '/authoring-roundtrip.siq';
    await download.saveAs(archivePath);
    assert.equal(await download.failure(), null);
    await visit('/studio');
    await page.getByLabel('Файл SIQ', { exact: true }).setInputFiles(archivePath);
    await page.getByRole('button', { name: 'Загрузить SIQ ↑', exact: true }).click();
    await page.waitForURL(/\/studio\/[a-f0-9]{32}$/, { timeout: 20000 });
    const imported = await api('GET', '/drafts/' + new URL(page.url()).pathname.split('/').pop());
    assert.deepEqual(shape(imported.document), shape(expected), 'SIQ roundtrip changed ordering or metadata');
    // Diagnostic content remains editable without deleting unsupported references/parameters.
    const diagnostic = document('Диагностика ' + suffix);
    diagnostic.rounds[0].themes[0].questions[0].params.push({ name: 'futureParameter', type: 'simple', value: 'retain-me' });
    diagnostic.rounds[0].themes[0].questions[0].params[0].items[0] = { type: 'image', value: 'missing.png', is_ref: true };
    const incomplete = await api('POST', '/drafts', { document: diagnostic });
    await visit('/studio/' + incomplete.card.id);
    await move('Фрагмент вопроса 1: ниже').click();
    diagnostic.rounds[0].themes[0].questions[0].params[0].items.reverse();
    await saved(incomplete.card.id, diagnostic);
    const retained = (await api('GET', '/drafts/' + incomplete.card.id)).document.rounds[0].themes[0].questions[0];
    assert.deepEqual(retained.params.find(p => p.name === 'futureParameter'), { name: 'futureParameter', type: 'simple', value: 'retain-me' });
    assert.equal(retained.params[0].items[1].is_ref, true);
    report.editor = { levels: 5, keyboard: true, stable_selection: true, undo: true, autosave_reload: true, ids_preserved: true, siq_roundtrip: true, diagnostic_preserved: true, mobile_overflow: false };
    console.log('[authoring] editor GREEN');
  }
  assert.deepEqual(errors, [], 'browser page errors');
  report.page_errors = 0;
  await writeFile(output + '/authoring-report.json', JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
} catch (error) {
  await page.screenshot({ path: output + '/authoring-failure.png', fullPage: true }).catch(() => {});
  console.error({ url: page.url(), errors });
  throw error;
} finally {
  if (registered) {
    const deleted = await context.request.delete(base + '/api/account/me', { data: { password } });
    assert.equal(deleted.status(), 200, 'synthetic account cleanup failed');
  }
  await context.close();
  await browser.close();
}
