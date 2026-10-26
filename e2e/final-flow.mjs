import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5175';
const answerMode = process.env.ANSWER_MODE || 'oral';
const noEligible = process.env.FINAL_NO_ELIGIBLE === '1';
const variant = noEligible ? 'no-eligible' : answerMode;
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' });
const owner = await browser.newContext();
const password = randomBytes(20).toString('hex');
const secret = 'FINAL_PRIVATE_' + randomBytes(8).toString('hex');
const answers = ['ALPHA_PRIVATE_', 'BETA_PRIVATE_'].map(s => s + randomBytes(8).toString('hex'));
const clients = [], errors = [];
let registered = false, checkpoint = 'setup';
const output = fileURLToPath(new URL('../.local/browser/', import.meta.url));
await mkdir(output, { recursive: true });
async function post(context, path, data) {
  const response = await context.request.post(base + '/api' + path, { data });
  assert.ok(response.ok(), `${path}: ${response.status()} ${await response.text()}`);
  return response.json();
}
async function until(predicate, message) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) { if (await predicate()) return; await new Promise(r => setTimeout(r, 40)); }
  throw new Error(`${checkpoint}: ${message}`);
}
async function client(context, credentials, name, display = false) {
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  await page.addInitScript(c => {
    sessionStorage.setItem('room:' + c.room_id, JSON.stringify(c));
    const NativeSocket = window.WebSocket;
    window.__testSockets = [];
    window.WebSocket = class extends NativeSocket { constructor(...args) { super(...args); window.__testSockets.push(this); } };
  }, credentials);
  const c = { page, credentials, name, display, state: null, wire: [], connections: 0 };
  clients.push(c);
  page.on('pageerror', e => errors.push(e.message));
  page.on('websocket', socket => {
    if (!display && new URL(socket.url()).searchParams.get('display') === '1') return;
    c.connections++;
    socket.on('framereceived', ({ payload }) => {
      try { const m = JSON.parse(payload.toString()); if (m.state) { c.wire.push(m.state); c.state = m.state; } } catch { /* heartbeat */ }
    });
  });
  await page.goto(`${base}/rooms/${credentials.room_id}${display ? '/display' : ''}`);
  await until(() => c.state?.gameplay, name + ' missing snapshot');
  return c;
}
const game = c => c.state.gameplay;
const publicStage = c => c.page.getByTestId('public-game-stage');
const finalStage = c => c.page.getByTestId('final-stage');
const button = (c, name) => c.page.getByRole('button', { name, exact: true });
const form = (c, title) => c.page.locator('form.game-action').filter({ has: c.page.getByRole('heading', { name: title, exact: true }) });
async function stage(c, kind) { await until(() => game(c).stage.kind === kind, `${c.name} expected ${kind}, got ${game(c).stage.kind}`); }
async function adjust(host, target, delta) {
  await button(host, 'Настройки и управление').click();
  const details = host.page.locator('details').filter({ has: host.page.locator('summary').filter({ hasText: /^Управление и апелляции$/ }) });
  if (!await details.evaluate(e => e.open)) await details.locator('summary').click();
  const controls = form(host, 'Изменить счёт');
  await controls.getByLabel(/^Участник/).selectOption(target.credentials.participant_id);
  await controls.getByLabel('Изменение очков').fill(String(delta));
  await controls.getByLabel('Причина').fill('Начальный счёт для проверки финала');
  const seq = host.state.seq;
  await controls.getByRole('button', { name: 'Изменить счёт', exact: true }).click();
  await until(() => host.state.seq > seq, 'score adjustment');
  await button(host, 'Закрыть').click();
}
async function noResults() {
  for (const c of clients) assert.equal(await c.page.getByTestId('final-results').count(), 0, `${c.name}: premature final results`);
}
async function privacy(viewers) {
  for (const c of viewers) {
    for (const snapshot of c.wire) {
      for (const sub of snapshot.gameplay?.submissions || []) {
        if (sub.revealed || sub.id === snapshot.you && !c.display) continue;
        assert.equal(Object.hasOwn(sub, 'wager'), false, `${c.name}: hidden wager leaked over WS`);
        assert.equal(Object.hasOwn(sub, 'answer'), false, `${c.name}: hidden answer leaked over WS`);
      }
    }
    const text = await publicStage(c).textContent();
    for (let i = 0; i < answers.length; i++) {
      const sub = game(c).submissions?.find(s => s.answer === answers[i] && s.revealed);
      if (!sub) assert.equal(text.includes(answers[i]), false, `${c.name}: hidden answer in public DOM`);
    }
  }
}
try {
  await post(owner, '/account/register', { username: 'final_' + randomBytes(6).toString('hex'), password, display_name: 'Ведущий финала' });
  registered = true;
  const draft = await post(owner, '/drafts', { document: { id: 'final-flow-fixture', name: 'Финал: общая сцена', version: '5.0', info: {}, rounds: [{ id: 'final', name: 'Большой финал', type: 'final', themes: ['Альфа', 'Бета', 'Гамма'].map((name, i) => ({ id: `t${i}`, name, questions: [{ id: `q${i}`, type: 'stakeAll', price: 700, params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: 'Публичный вопрос финала' }] }], right: [secret] }] })) }] } });
  const credentials = await post(owner, '/rooms', { host_name: 'Ведущий финала', pack_id: draft.card.id, revision: draft.card.revision, rules: { preset: 'classic', presentation: true, answer_mode: answerMode, auto_open: true, selection_seconds: 120, reading_seconds: 1, answer_seconds: 120, special_seconds: 120, wager_seconds: 120, final_seconds: 120, penalties: true, buzzer_mode: 'first', window_ms: 200, false_start: true, false_start_seconds: 3, no_risk_multiplier: 2 } });
  const host = await client(owner, credentials, 'host');
  const players = [];
  for (const name of ['Игрок Альфа', 'Игрок Бета', 'Минус 東京 LongName']) {
    const context = await browser.newContext();
    const joined = await post(context, `/rooms/${credentials.room_id}/participants`, { name, role: 'player', invite_token: credentials.invite_token });
    players.push(await client(context, joined, name));
  }
  const spectatorContext = await browser.newContext();
  const spectatorCredentials = await post(spectatorContext, `/rooms/${credentials.room_id}/participants`, { name: 'Зритель финала', role: 'spectator', invite_token: credentials.invite_token });
  const spectator = await client(spectatorContext, spectatorCredentials, 'spectator');
  const display = await client(owner, credentials, 'display', true);
  for (let i = 0; i < players.length; i++) await adjust(host, players[i], (noEligible ? [0, 0, -50] : [100, 300, -50])[i]);
  await button(host, 'Начать раунд').click();
  await stage(host, 'round_intro');
  await button(host, 'Пропустить заставку').click();
  if (noEligible) {
    checkpoint = 'no eligible players';
    await stage(host, 'resolved');
    assert.deepEqual(game(host).final.eligible_ids, []);
    assert.equal(game(host).question == null, true);
    for (const c of [host, display]) await finalStage(c).getByRole('heading', { name: 'Нет игроков с положительным счётом', exact: true }).waitFor();
  } else {
  await stage(host, 'eliminate_theme');
  checkpoint = 'final presentation contract';
  for (const c of [host, display, spectator]) await finalStage(c).waitFor();
  assert.deepEqual(new Set(game(host).final.eligible_ids), new Set(players.slice(0, 2).map(p => p.credentials.participant_id)));
  await noResults();
  await host.page.screenshot({ path: output + `ux12-${variant}-themes.png` });
  for (let remaining = 3; remaining > 1; remaining--) {
    const active = players.find(p => p.credentials.participant_id === game(host).stage.turn_id);
    await stage(active, 'eliminate_theme');
    const controls = form(active, 'Удалить тему финала');
    await controls.getByLabel(/^Тема/).selectOption(game(host).final.themes[0].id);
    await controls.getByRole('button', { name: 'Удалить тему финала', exact: true }).click();
    await until(() => game(host).final.themes.length === remaining - 1, 'theme deletion');
  }
  await stage(host, 'select_final');
  await button(host, `${game(host).final.themes[0].name}, 700 очков`).click();
  await stage(host, 'collect_wagers');
  checkpoint = 'private wagers and refresh';
  await button(host, 'Пауза Ⅱ').click();
  await until(() => clients.every(c => c.state.phase === 'paused'), 'pause not synchronized');
  const count = display.wire.length;
  await display.page.reload();
  await until(() => display.wire.length > count && display.state.phase === 'paused', 'refresh did not restore pause');
  await finalStage(display).waitFor();
  await button(host, 'Продолжить ▶').click();
  for (const p of players.slice(0, 2)) {
    const controls = form(p, 'Ваша закрытая ставка');
    await controls.getByLabel('Сумма').fill('100');
    await controls.getByRole('button', { name: 'Зафиксировать ставку', exact: true }).click();
    await until(() => game(host).players.some(s => s.id === p.credentials.participant_id && s.wager_submitted), 'wager missing');
  }
  await stage(host, 'present_question');
  await privacy([spectator, display, ...players]);
  await button(host, 'Продолжить →').click();
  await stage(host, 'collect_answers');
  for (const c of [host, spectator, display]) {
    await publicStage(c).getByRole('heading', { name: 'Публичный вопрос финала', exact: true }).waitFor();
    assert.equal((await publicStage(c).textContent()).includes(secret), false, 'correct answer leaked');
  }
  if (answerMode === 'oral') await button(host, 'Закрыть сбор ответов').click();
  else for (let i = 0; i < 2; i++) {
    const controls = form(players[i], 'Ваш ответ');
    await controls.getByLabel('Текст ответа').fill(answers[i]);
    await controls.getByRole('button', { name: 'Отправить ответ', exact: true }).click();
    await until(() => game(host).players.some(s => s.id === players[i].credentials.participant_id && s.answer_submitted), 'answer missing');
  }
  await stage(host, 'reveal');
  await privacy([spectator, display, ...players]);
  await noResults();
  checkpoint = 'sequential reveal';
  for (let i = 0; i < 2; i++) {
    const target = players.find(p => p.credentials.participant_id === game(host).stage.turn_id);
    await button(host, `Раскрыть: ${target.name}`).click();
    await stage(host, 'judge');
    for (const c of [host, spectator, display]) {
      await finalStage(c).getByTestId('final-player-result').filter({ hasText: target.name }).waitFor();
      if (answerMode === 'written') await finalStage(c).getByText(answers[players.indexOf(target)], { exact: true }).waitFor();
    }
    await privacy([spectator, display, ...players]);
    await noResults();
    await button(host, target === players[0] ? '✓ Верно' : '× Неверно').click();
    await stage(host, i === 0 ? 'reveal' : 'resolved');
  }
  }
  assert.notEqual(host.state.phase, 'finished', 'final judgement silently finished game');
  await noResults();
  checkpoint = 'finished rankings';
  await button(host, 'Завершить игру').click();
  await button(host, 'Завершить').click();
  await until(() => clients.every(c => c.state.phase === 'finished'), 'finish not synchronized');
  for (const c of [host, display]) {
    const results = c.page.getByTestId('final-results');
    await results.waitFor();
    const rows = results.getByTestId('final-ranking-row');
    assert.equal(await rows.count(), 3);
    assert.deepEqual(await rows.evaluateAll(es => es.map(e => e.dataset.rank)), ['1', '1', '3']);
    assert.deepEqual(await rows.evaluateAll(es => es.map(e => e.dataset.playerId)), players.map(p => p.credentials.participant_id));
    assert.deepEqual(await rows.locator('strong').evaluateAll(es => es.map(e => Number(e.childNodes[0].textContent))), noEligible ? [0, 0, -50] : [200, 200, -50]);
    assert.equal(await results.evaluate(e => e.scrollWidth > e.clientWidth), false, 'ranking overflows horizontally');
    const bounds = await results.boundingBox();
    assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= c.page.viewportSize().height, `${c.name}: entire results card must fit viewport: ${JSON.stringify(bounds)}`);
    if (c.display) {
      const fullscreen = button(c, 'На весь экран');
      assert.equal(await fullscreen.isVisible(), true, 'fullscreen missing after finish');
      const fullscreenBounds = await fullscreen.boundingBox();
      assert.ok(fullscreenBounds && fullscreenBounds.y >= 0 && fullscreenBounds.y + fullscreenBounds.height <= c.page.viewportSize().height, 'fullscreen outside viewport after finish');
    }
    const capture = { variant, client: c.name, phase: c.state.phase, viewport: c.page.viewportSize(), scrollY: await c.page.evaluate(() => window.scrollY), results: await results.boundingBox(), fullscreen: c.display ? await button(c, 'На весь экран').boundingBox() : null, capturedAt: new Date().toISOString() };
    assert.equal(capture.scrollY, 0, 'finished screenshot must show the top of the page');
    await c.page.screenshot({ path: output + `ux12-${variant}-${c.name}-results.png` });
    await writeFile(output + `ux12-${variant}-${c.name}-results.json`, JSON.stringify(capture, null, 2) + '\n');
  }
  await display.page.reload();
  await display.page.getByTestId('final-results').waitFor();
  await privacy([spectator, display, ...players]);
  assert.deepEqual(errors, [], 'browser runtime errors');
  console.log(`PASS UX12 (${variant}): ${noEligible ? 'no eligible players, clear final explanation, explicit finish, zero/negative tied ranking' : 'theme elimination, closed wagers and answers, sequential reveal/judge, explicit finish, shared tied rankings and negative score, pause/reload, viewer WS privacy'}.`);
} catch (error) {
  console.error('Checkpoint:', checkpoint);
  for (const c of clients.filter(c => ['host', 'display'].includes(c.name))) {
    console.error(c.name, JSON.stringify({ phase: c.state?.phase, stage: c.state?.gameplay?.stage, text: await publicStage(c).textContent() }));
    await c.page.screenshot({ path: output + `ux12-${variant}-${c.name}-failure.png` }).catch(() => {});
  }
  throw error;
} finally {
  if (registered) await owner.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
