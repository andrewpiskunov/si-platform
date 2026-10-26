import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5174';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
const owner = await browser.newContext();
const password = randomBytes(20).toString('hex');
const secrets = ['PRIVATE_FIRST_' + randomBytes(8).toString('hex'), 'PRIVATE_SECOND_' + randomBytes(8).toString('hex')];
const replic = 'HOST_REPLIC_' + randomBytes(8).toString('hex');
const answerMode = process.env.ANSWER_MODE || 'oral';
const submission = 'PRIVATE_SUBMISSION_' + randomBytes(8).toString('hex');
const errors = [];
let registered = false;
const observedClients = [];
async function request(context, path, data) {
  const response = await context.request.post(base + '/api' + path, { data });
  assert.ok(response.ok(), `${path}: ${response.status()} ${await response.text()}`);
  return response.json();
}
async function until(predicate, message) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(message);
}
async function client(context, credentials, display = false) {
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  await page.addInitScript(c => {
    sessionStorage.setItem('room:' + c.room_id, JSON.stringify(c));
    // Close an actual transport below; the application must establish a new socket.
    const NativeSocket = window.WebSocket;
    window.__testSockets = [];
    window.WebSocket = class extends NativeSocket {
      constructor(...args) { super(...args); window.__testSockets.push(this); }
    };
  }, credentials);
  const c = { page, states: [], connections: 0, state: null, isHost: !display && context === owner };
  observedClients.push(c);
  page.on('pageerror', error => errors.push(error.message));
  page.on('websocket', socket => {
    if (!display && new URL(socket.url()).searchParams.get('display') === '1') return;
    c.connections++;
    socket.on('framereceived', ({ payload }) => {
      try {
        const message = JSON.parse(payload.toString());
        if (message.state) { c.states.push(message.state); c.state = message.state; }
      } catch { /* Non-JSON heartbeat. */ }
    });
  });
  await page.goto(`${base}/rooms/${credentials.room_id}${display ? '/display' : ''}`);
  await until(() => c.state?.gameplay, 'Missing live room snapshot');
  return c;
}
const stage = c => c.page.getByTestId('public-game-stage');
const button = (c, name) => c.page.getByRole('button', { name, exact: true });
async function sameBoard(clients) {
  const boards = await Promise.all(clients.map(c => stage(c).locator('.board').innerText()));
  for (const board of boards.slice(1)) assert.equal(board, boards[0], 'Public board differs across roles');
}
async function privacy(clients, secret) {
  for (const c of clients) {
    assert.equal((await stage(c).textContent()).includes(secret), false, 'Private answer leaked into public stage');
    assert.equal(await stage(c).locator('.host-console, .host-answer, input, textarea').count(), 0, 'Private controls inside public stage');
  }
  for (const c of clients.filter(c => !c.isHost)) {
    assert.equal((await c.page.locator('body').textContent()).includes(secret), false, 'Private answer leaked to viewer DOM');
    assert.equal(c.states.some(state => JSON.stringify(state).includes(secret)), false, 'Private answer leaked over viewer websocket');
  }
}
try {
  await request(owner, '/account/register', { username: 'stage_' + randomBytes(6).toString('hex'), password, display_name: 'Ведущий' });
  registered = true;
  const draft = await request(owner, '/drafts', { document: {
    id: 'public-stage-fixture', name: 'Общая публичная сцена', version: '5.0', info: {},
    rounds: [{ id: 'r1', name: 'Раунд проверки', themes: [{ id: 't1', name: 'Синхронизация', questions: secrets.map((answer, i) => ({
      id: `q${i + 1}`, price: (i + 1) * 100, type: 'simple',
      params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: replic, placement: 'replic', duration_ms: 1000 }, { type: 'text', value: `Публичный вопрос ${i + 1}` }] }], right: [answer]
    })) }] }]
  } });
  const credentials = await request(owner, '/rooms', { host_name: 'Ведущий', pack_id: draft.card.id, revision: draft.card.revision,
    rules: { preset: 'classic', presentation: true, answer_mode: answerMode, auto_open: true, reading_seconds: 1, selection_seconds: 120, answer_seconds: 120, special_seconds: 120, wager_seconds: 120, final_seconds: 120, penalties: true, buzzer_mode: 'first', window_ms: 200, false_start: true, false_start_seconds: 3, no_risk_multiplier: 2 } });
  const host = await client(owner, credentials);
  const playerContext = await browser.newContext();
  const joined = await request(playerContext, `/rooms/${credentials.room_id}/participants`, { name: 'Игрок сцены', role: 'player', invite_token: credentials.invite_token });
  const player = await client(playerContext, joined);
  const secondContext = await browser.newContext();
  const secondJoined = await request(secondContext, `/rooms/${credentials.room_id}/participants`, { name: 'Второй игрок', role: 'player', invite_token: credentials.invite_token });
  const secondPlayer = await client(secondContext, secondJoined);
  const secondHost = await client(owner, credentials);
  const display = await client(owner, credentials, true);
  const secondDisplay = await client(owner, credentials, true);
  const clients = [host, player, display, secondHost, secondDisplay];
  await display.page.getByRole('heading', { name: 'Собираемся играть', exact: true }).waitFor();
  assert.equal(await display.page.locator('.host-console, .host-answer').count(), 0);
  await display.page.bringToFront();
  await button(display, 'На весь экран').click();
  await until(() => display.page.evaluate(() => !!document.fullscreenElement), 'Fullscreen did not activate');
  await display.page.evaluate(() => document.exitFullscreen());
  await button(host, 'Начать раунд').click();
  for (const c of clients) await stage(c).locator('[data-scene="round_intro"]').waitFor();
  await button(host, 'Пауза Ⅱ').click();
  await display.page.reload();
  for (const c of clients) await stage(c).locator('[data-scene="round_intro"].paused').waitFor();
  await new Promise(resolve => setTimeout(resolve, 3200));
  for (const c of clients) assert.equal(await stage(c).locator('[data-scene="round_intro"].paused').count(), 1, 'Paused introduction advanced');
  await button(host, 'Продолжить ▶').click();
  await button(host, 'Пропустить заставку').click();
  for (const c of clients) await stage(c).locator('.board').waitFor();
  await sameBoard(clients);
  await button(host, 'Синхронизация, 100 очков').click();
  for (const c of clients) await stage(c).locator('[data-scene="question_price"]').waitFor();
  await host.page.locator('.host-answer').getByText(secrets[0], { exact: true }).waitFor();
  for (const c of clients) await stage(c).getByRole('heading', { name: 'Публичный вопрос 1', exact: true }).waitFor();
  await privacy(clients, secrets[0]);
  const output = fileURLToPath(new URL('../.local/browser/', import.meta.url));
  await mkdir(output, { recursive: true });
  await host.page.screenshot({ path: output + 'ux11-host.png', animations: 'disabled' });
  await display.page.screenshot({ path: output + 'ux11-display.png', animations: 'disabled' });
  await privacy(clients, replic);
  assert.ok((await host.page.locator('.host-console .host-question-notes').textContent()).includes(replic), 'Host lost private presenter note');
  await button(host, 'Пауза Ⅱ').click();
  await until(() => clients.every(c => c.state.phase === 'paused'), 'Pause did not reach all clients');
  const connections = display.connections;
  const snapshots = display.states.length;
  await display.page.evaluate(() => window.__testSockets.forEach(socket => socket.close()));
  await until(() => display.connections > connections, 'Display did not reconnect after socket loss');
  await until(() => display.states.length > snapshots && display.state.phase === 'paused', 'Display did not receive fresh paused snapshot on reconnect');
  await privacy(clients, secrets[0]);
  await button(host, 'Продолжить ▶').click();
  await button(player, 'Знаю ответ!').click();
  if (answerMode === 'written') {
    await player.page.getByLabel('Текст ответа', { exact: true }).fill(submission);
    await button(player, 'Отправить ответ').click();
    await until(() => host.state.gameplay.stage.kind === 'judge', 'Written submission did not reach host judgement');
    await host.page.getByText(submission, { exact: true }).waitFor();
    await privacy([host, secondHost, display, secondDisplay, secondPlayer], submission);
    assert.equal((await stage(player).textContent()).includes(submission), false, 'Own private submission leaked into player public stage');
  }
  await host.page.locator('.host-console').getByRole('button', { name: '✓ Верно', exact: true }).click();
  for (const c of clients) {
    await stage(c).getByText(secrets[0], { exact: true }).first().waitFor();
    await c.page.locator('.score-item').filter({ hasText: 'Игрок сцены' }).getByText('Верно · +100', { exact: true }).waitFor();
    assert.equal(await c.page.locator('.score-item').filter({ hasText: 'Игрок сцены' }).locator('strong').innerText(), '100');
  }
  await button(host, 'Синхронизация, 200 очков').click();
  for (const c of clients) await stage(c).getByRole('heading', { name: 'Публичный вопрос 2', exact: true }).waitFor();
  await host.page.locator('.host-answer').getByText(secrets[1], { exact: true }).waitFor();
  await display.page.reload();
  await stage(display).getByRole('heading', { name: 'Публичный вопрос 2', exact: true }).waitFor();
  for (const c of clients) assert.equal(await stage(c).getByRole('heading', { name: 'Публичный вопрос 1', exact: true }).count(), 0, 'Previous question remained after transition');
  await privacy(clients, secrets[1]);
  assert.deepEqual(errors, [], 'Browser runtime errors');
  console.log(`PASS (${answerMode}, two host tabs + two displays): public board/question/introduction/answer/score across host, player and display; private answer absent in public DOM and viewer WS; fullscreen, pause, reload, socket reconnect and question change${answerMode === 'written' ? '; private written submission outside public stage and viewer WS' : ''}`);
} catch (error) {
  for (const c of observedClients) console.error(JSON.stringify({ url: c.page.url(), phase: c.state?.phase, stage: c.state?.gameplay?.stage, text: await stage(c).evaluateAll(elements => elements[0]?.textContent || 'stage missing') }));
  throw error;
} finally {
  if (registered) await owner.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
