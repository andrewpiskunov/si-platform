import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5175';
const mode = process.env.REVEAL_MODE || 'enabled';
const enabled = mode === 'enabled';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' });
const owner = await browser.newContext();
const password = randomBytes(20).toString('hex');
const privateAnswer = 'PRIVATE_ANSWER_' + randomBytes(8).toString('hex');
const privateReplic = 'PRIVATE_REPLIC_' + randomBytes(8).toString('hex');
const previousText = 'Начало 😀 東京.';
const currentText = 'Продолжение 🌏: ' + 'Назовите столицу страны и объясните свой ответ. '.repeat(5);
const output = fileURLToPath(new URL('../.local/browser/', import.meta.url));
const clients = [], errors = [];
let registered = false, checkpoint = 'setup';
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
  const page = await context.newPage(); page.setDefaultTimeout(15000);
  await page.addInitScript(c => sessionStorage.setItem('room:' + c.room_id, JSON.stringify(c)), credentials);
  const c = { page, credentials, name, display, state: null, wire: [] }; clients.push(c);
  page.on('pageerror', e => errors.push(e.message));
  page.on('websocket', socket => {
    if (!display && new URL(socket.url()).searchParams.get('display') === '1') return;
    socket.on('framereceived', ({ payload }) => {
      try { const m = JSON.parse(payload.toString()); if (m.state) { c.state = m.state; c.wire.push(m.state); } } catch { /* heartbeat */ }
    });
  });
  await page.goto(`${base}/rooms/${credentials.room_id}${display ? '/display' : ''}`);
  await until(() => c.state?.gameplay, `${name} snapshot`); return c;
}
const game = c => c.state.gameplay;
const stage = c => c.page.getByTestId('public-game-stage');
const reveal = c => stage(c).getByTestId('question-reveal');
const visible = c => reveal(c).locator('.question-visible');
const button = (c, name) => c.page.getByRole('button', { name, exact: true });
async function privacy(viewers) {
  for (const c of viewers) {
    assert.equal(c.wire.some(s => JSON.stringify(s).includes(privateAnswer)), false, `${c.name}: private answer on WS`);
    assert.equal(c.wire.some(s => JSON.stringify(s).includes(privateReplic)), false, `${c.name}: private replic on WS`);
    for (const secret of [privateAnswer, privateReplic]) assert.equal((await stage(c).textContent()).includes(secret), false, `${c.name}: private content in stage`);
  }
}
try {
  await post(owner, '/account/register', { username: 'reveal_' + randomBytes(6).toString('hex'), password, display_name: 'Ведущий' }); registered = true;
  if (enabled) {
    const settingsPage = await owner.newPage();
    await settingsPage.goto(base + '/rooms');
    await settingsPage.waitForLoadState('networkidle');
    await settingsPage.getByRole('button', { name: 'Создать комнату', exact: true }).first().click();
    const dialog = settingsPage.getByRole('dialog', { name: 'Создать комнату' });
    await dialog.waitFor();
    await dialog.locator('summary').filter({ hasText: 'Правила и таймеры' }).click();
    const setting = dialog.getByRole('checkbox', { name: 'Постепенно раскрывать вопросы', exact: true });
    assert.equal(await setting.isChecked(), true, 'new room default should enable reveal');
    await setting.uncheck(); assert.equal(await setting.isChecked(), false);
    await setting.check(); assert.equal(await setting.isChecked(), true);
    await settingsPage.close();
  }
  const draft = await post(owner, '/drafts', { document: { id: 'reveal-fixture', name: 'Постепенный показ', version: '5.0', info: {}, rounds: [{ id: 'r1', name: 'Показ', themes: [{ id: 't1', name: 'Текст', questions: [{ id: 'q1', price: 100, type: 'simple', params: [{ name: 'question', type: 'content', items: [
    { type: 'text', value: previousText, duration_ms: 1000 },
    { type: 'text', value: privateReplic, placement: 'replic', duration_ms: 1000 },
    { type: 'text', value: currentText, duration_ms: 12000 }
  ] }], right: [privateAnswer] }] }] }] } });
  const rules = { preset: 'classic', presentation: false, answer_mode: 'oral', auto_open: true, reading_seconds: 12, selection_seconds: 120, answer_seconds: 120, special_seconds: 120, wager_seconds: 120, final_seconds: 120, penalties: true, buzzer_mode: 'first', window_ms: 200, false_start: false, false_start_seconds: 3, no_risk_multiplier: 2, ...(mode === 'legacy' ? {} : { progressive_reveal: enabled }) };
  const credentials = await post(owner, '/rooms', { host_name: 'Ведущий', pack_id: draft.card.id, revision: draft.card.revision, rules });
  const host = await client(owner, credentials, 'host');
  const players = [];
  for (let i = 0; i < 2; i++) {
    const ctx = await browser.newContext();
    const joined = await post(ctx, `/rooms/${credentials.room_id}/participants`, { name: `Игрок ${i}`, role: 'player', invite_token: credentials.invite_token });
    players.push(await client(ctx, joined, `player${i}`));
  }
  const display = await client(owner, credentials, 'display', true);
  const reducedContext = await browser.newContext({ reducedMotion: 'reduce' });
  const reduced = await client(reducedContext, credentials, 'reduced-display', true);
  await button(host, 'Начать раунд').click();
  await button(host, 'Текст, 100 очков').click();
  checkpoint = 'third public fragment';
  await until(() => clients.every(c => game(c).stage.kind === 'present_question' && game(c).question.content_index === 2), 'final text fragment missing');
  for (const c of [host, display, reduced]) await reveal(c).waitFor();
  assert.equal(Boolean(game(display).rules.progressive_reveal), enabled, 'rule not preserved');
  assert.equal(await players[0].page.locator('.buzzer-dock .buzz').isDisabled(), true, 'reading stage unexpectedly opens buzzer');
  const publicText = previousText + '\n' + currentText;
  if (enabled) {
    const metadata = game(display).question.reveal;
    assert.equal(metadata?.kind, 'text', 'missing server reveal metadata');
    assert.equal(metadata.duration_ms, 12000);
    assert.equal(metadata.text_offset, Array.from(previousText + '\n').length, 'offset must count Unicode codepoints and omit replic');
    for (const c of [host, display]) {
      const text = await visible(c).textContent();
      assert.ok(text.startsWith(previousText + '\n'), 'prior fragment must remain fully visible');
      assert.ok(text.length < publicText.length, 'current text unexpectedly fully visible at start');
      assert.equal(await reveal(c).locator('.question-concealed').evaluate(e => getComputedStyle(e).visibility), 'hidden');
    }
    const layout = c => reveal(c).locator('h2').evaluate(e => ({ fontSize: getComputedStyle(e).fontSize, width: e.getBoundingClientRect().width, height: e.getBoundingClientRect().height }));
    await new Promise(r => setTimeout(r, 120));
    const initialLayout = await Promise.all([host, display].map(layout));
    const before = await visible(display).textContent();
    await until(async () => (await visible(display).textContent()).length > before.length + 10, 'text did not reveal over time');
    checkpoint = 'pause freezes public text';
    await button(host, 'Пауза Ⅱ').click();
    await until(() => clients.every(c => c.state.phase === 'paused'), 'pause not synchronized');
    await new Promise(r => setTimeout(r, 100));
    const frozen = await visible(display).textContent();
    const elapsed = game(display).question.reveal.elapsed_ms;
    const start = game(display).question.reveal.start_at_ms;
    await new Promise(r => setTimeout(r, 700));
    assert.equal(await visible(display).textContent(), frozen, 'text advanced during pause');
    assert.equal(await visible(host).textContent(), frozen, 'host public preview differs at same frozen clock');
    assert.deepEqual(await Promise.all([host, display].map(layout)), initialLayout, 'text reveal changed heading bounds/font size');
    const reducedProgress = Number(await reveal(reduced).getAttribute('data-progress'));
    assert.equal((reducedProgress * 4) % 1, 0, 'reduced motion must use coarse quarter steps');
    assert.ok(reducedProgress <= Number(await reveal(display).getAttribute('data-progress')));
    const count = display.wire.length;
    await display.page.reload();
    await until(() => display.wire.length > count && display.state.phase === 'paused', 'paused reload snapshot missing');
    await reveal(display).waitFor();
    assert.equal(game(display).question.reveal.elapsed_ms, elapsed, 'paused metadata changed after reload');
    assert.equal(await visible(display).textContent(), frozen, 'paused reload restarted or completed reveal');
    await display.page.screenshot({ path: output + 'ux13-text-paused-display.png' });
    await host.page.screenshot({ path: output + 'ux13-text-paused-host.png' });
    await privacy([display, reduced, ...players]);
    await button(host, 'Продолжить ▶').click();
    await until(() => game(display).question.reveal.start_at_ms > start, 'resume did not shift reveal start');
    await until(async () => (await visible(display).textContent()).length > frozen.length + 10, 'resume did not continue reveal');
    const priorReload = (await visible(display).textContent()).length;
    await display.page.reload();
    await reveal(display).waitFor();
    assert.ok((await visible(display).textContent()).length >= priorReload - 5, 'live reload restarted reveal');
  } else {
    assert.equal(game(display).question.reveal, undefined, 'disabled/legacy room receives reveal metadata');
    for (const c of [host, display, reduced]) assert.equal(await visible(c).textContent(), publicText, 'disabled/legacy text must appear immediately');
  }
  checkpoint = 'normal buzzer after reading';
  await until(() => clients.every(c => game(c).stage.kind === 'buzz_open'), 'normal reading deadline did not open buzzer');
  assert.equal(await visible(display).textContent(), publicText, 'full text missing after reading');
  await button(players[0], 'Знаю ответ!').click();
  await until(() => game(host).stage.kind === 'collect_answer', 'buzzer did not transfer answer turn');
  await host.page.locator('.host-console').getByRole('button', { name: '✓ Верно', exact: true }).waitFor();
  await privacy([display, reduced, ...players]);
  await display.page.screenshot({ path: output + `ux13-${mode}-complete-display.png` });
  assert.deepEqual(errors, [], 'browser runtime errors');
  console.log(`PASS UX13 ${mode}: real API/WS text fragment, Unicode offset, private replicas/answer, ${enabled ? 'progression, frozen pause/reload, resume/live reload, reduced motion, ' : 'immediate legacy display, '}unchanged buzzer timing. Image processing not covered.`);
} catch (error) {
  console.error('Checkpoint:', checkpoint);
  for (const c of clients.filter(c => c.name === 'host' || c.name === 'display')) {
    console.error(c.name, JSON.stringify({ phase: c.state?.phase, game: c.state?.gameplay?.stage, question: c.state?.gameplay?.question }));
    await c.page.screenshot({ path: output + `ux13-${mode}-${c.name}-failure.png` }).catch(() => {});
  }
  throw error;
} finally {
  if (registered) await owner.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
