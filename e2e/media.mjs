import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdtemp, readFile, writeFile, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5173';
const output = process.env.ARTIFACT_DIR || fileURLToPath(new URL('../.local/browser', import.meta.url));
await mkdir(output, { recursive: true });
const fixtureDir = await mkdtemp(join(tmpdir(), 'si-media-browser-'));
const ffmpeg = process.env.MEDIA_FFMPEG || 'ffmpeg';
const run = promisify(execFile);
async function fixture(name, args) {
  const path = join(fixtureDir, name);
  await run(ffmpeg, ['-v', 'error', '-nostdin', '-y', ...args, '-threads', '1', '-filter_threads', '1', path], { timeout: 30000 });
  return { name, path, data: await readFile(path) };
}
const picture = await fixture('question.png', ['-f', 'lavfi', '-i', 'testsrc=size=320x180:rate=1', '-frames:v', '1']);
const sound = await fixture('sound.wav', ['-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=24000', '-t', '8', '-c:a', 'pcm_s16le']);
const movie = await fixture('movie.mp4', ['-f', 'lavfi', '-i', 'testsrc=size=320x180:rate=12', '-f', 'lavfi', '-i', 'sine=frequency=660:sample_rate=24000', '-t', '8', '-pix_fmt', 'yuv420p', '-c:v', 'libx264', '-c:a', 'aac', '-movflags', '+faststart']);
const answer = await fixture('private-answer.png', ['-f', 'lavfi', '-i', 'color=blue:size=96x64', '-frames:v', '1']);
const browser = await chromium.launch();
const contexts = [], clients = [], pageErrors = [];
const suffix = randomBytes(8).toString('hex'), password = randomBytes(20).toString('hex');
let ownerContext, registered = false, checkpoint = 'setup', releaseSlow = () => {};
async function request(context, method, path, data, token) {
  const response = await context.request.fetch(base + '/api' + path, { method, data, ...(token ? { headers: { Authorization: `Bearer ${token}` } } : {}) });
  assert.ok(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`);
  return response.status() === 204 ? null : response.json();
}
async function until(predicate, message, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`${checkpoint}: ${message}`);
}
async function upload(context, draft, file, kind) {
  let job = await request(context, 'POST', '/media-uploads', { name: file.name, size: file.data.length, kind });
  const response = await context.request.patch(`${base}/api/uploads/${job.id}`, { data: file.data, headers: { 'Content-Type': 'application/octet-stream', 'Upload-Offset': '0' } });
  assert.equal(response.status(), 200, await response.text());
  await request(context, 'POST', `/uploads/${job.id}/complete`);
  await until(async () => {
    job = await request(context, 'GET', `/uploads/${job.id}`);
    assert.notEqual(job.status, 'failed', `media processing ${job.error}`);
    return job.status === 'ready';
  }, `${kind} did not prepare`, 30000);
  assert.ok(job.asset?.prepared?.standard && job.asset?.prepared?.economy);
  return request(context, 'POST', `/drafts/${draft.card.id}/assets`, { upload_id: job.id, base_revision: draft.card.revision });
}
async function newClient(credentials, name, context) {
  context ||= await browser.newContext(name === 'Мобильный игрок' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {});
  if (!contexts.includes(context)) contexts.push(context);
  await context.addInitScript(({ credentials }) => sessionStorage.setItem('room:' + credentials.room_id, JSON.stringify(credentials)), { credentials });
  const page = await context.newPage(), client = { credentials, name, context, page, state: null, wire: [], mediaRequests: [], ticketRequests: [] };
  clients.push(client);
  page.on('pageerror', error => pageErrors.push({ client: name, message: error.message }));
  page.on('request', req => { if (/\/rooms\/[^/]+\/media\//.test(req.url())) client.mediaRequests.push(new URL(req.url()).pathname); if (req.url().endsWith('/media-ticket')) client.ticketRequests.push(req.postDataJSON()); });
  page.on('websocket', socket => socket.on('framereceived', ({ payload }) => {
    try { const message = JSON.parse(payload.toString()); if (message.state) { client.wire.push(message.state); if (!client.state || message.state.seq >= client.state.seq) client.state = message.state; } } catch { /* heartbeat */ }
  }));
  await page.goto(`${base}/rooms/${credentials.room_id}`);
  await until(() => client.state?.gameplay?.version === 2, `${name}: no snapshot`);
  await page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
  return client;
}
const game = c => c.state.gameplay;
async function stage(c, kind) { await until(() => game(c)?.stage.kind === kind, `${c.name}: expected ${kind}, got ${game(c)?.stage.kind}`); }
async function button(c, name) { await c.page.getByRole('button', { name, exact: true }).click(); }
async function playing(c, tag) {
  await until(() => c.page.locator(`.media-player ${tag}`).evaluateAll(els => els.length === 1 && els[0].readyState >= 2), `${c.name}: ${tag} not ready`);
  const start = c.page.getByRole('button', { name: 'Включить воспроизведение', exact: true });
  if (await start.count()) await start.click();
  await until(() => c.page.locator(`.media-player ${tag}`).evaluate(el => !el.paused), `${c.name}: ${tag} did not play`);
}
try {
  ownerContext = await browser.newContext(); contexts.push(ownerContext);
  await request(ownerContext, 'POST', '/account/register', { username: 'media_' + suffix, password, display_name: 'Медиа-автор' }); registered = true;
  const qID = randomUUID(), roundID = randomUUID(), themeID = randomUUID();
  const question = { id: qID, type: 'simple', price: 100, info: {}, params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: 'Медиа-проверка' }] }], right: ['Ответ'] };
  const document = { id: randomUUID(), version: '5.0', name: 'Браузерный медиапакет', language: 'ru', info: {}, rounds: [{ id: roundID, name: 'Медиа', type: 'standard', info: {}, themes: [{ id: themeID, name: 'Подготовленные файлы', info: {}, questions: [question] }] }] };
  let draft = await request(ownerContext, 'POST', '/drafts', { document });
  checkpoint = 'prepare and attach originals';
  for (const [file, kind] of [[picture, 'image'], [sound, 'audio'], [movie, 'video'], [answer, 'image']]) draft = await upload(ownerContext, draft, file, kind);
  assert.equal(draft.assets.length, 4);
  const asset = name => draft.assets.find(a => a.name.endsWith('-' + name));
  const content = (kind, name) => ({ type: kind, value: asset(name).name.split('/').slice(1).join('/'), is_ref: true, wait_for_finish: true });
  question.params[0].items = [content('image', picture.name), content('audio', sound.name), content('video', movie.name)];
  question.params.push({ name: 'answer', type: 'content', items: [content('image', answer.name)] });
  for (const [kind, file, price] of [['audio', sound, 200], ['video', movie, 300]]) {
    document.rounds[0].themes[0].questions.push({ id: randomUUID(), type: 'simple', price, info: {},
      params: [{ name: 'question', type: 'content', items: [{ ...content(kind, file.name), duration_ms: 2000, wait_for_finish: false }] }], right: ['Ответ'] });
  }
  draft = await request(ownerContext, 'PUT', `/drafts/${draft.card.id}`, { base_revision: draft.card.revision, document });
  assert.equal((draft.play_issues || []).filter(i => i.severity === 'error').length, 0);
  checkpoint = 'editor prepared asset preview';
  const editor = await ownerContext.newPage(); editor.on('pageerror', err => pageErrors.push({ client: 'editor', message: err.message }));
  await editor.goto(`${base}/studio/${draft.card.id}`);
  await editor.getByText('Браузерный медиапакет', { exact: true }).first().waitFor();
  await editor.getByRole('heading', { name: 'Медиафайлы', exact: true }).waitFor();
  checkpoint = 'upload from editor with unsaved changes';
  const localTitle = 'Медиа — локальная правка';
  await editor.getByLabel('Название', { exact: true }).fill(localTitle);
  await editor.getByLabel(/^Тип медиа/).selectOption('image');
  await editor.getByLabel('Медиафайл', { exact: true }).setInputFiles(answer.path);
  await editor.getByRole('button', { name: 'Загрузить и прикрепить', exact: true }).click();
  await until(async () => {
    draft = await request(ownerContext, 'GET', `/drafts/${draft.card.id}`);
    return draft.assets.length === 5;
  }, 'editor upload did not attach', 30000);
  assert.equal(draft.document.name, localTitle, 'upload discarded unsaved editor text');
  const firstPreview = editor.locator('.media-library details').first();
  await firstPreview.locator('summary').click();
  await until(() => firstPreview.locator('img').evaluateAll(imgs => imgs.length === 1 && imgs[0].complete && imgs[0].naturalWidth > 0), 'standard preview did not load');
  await editor.getByLabel(/^Качество предпросмотра/).selectOption('economy');
  await until(() => firstPreview.locator('img').evaluateAll(imgs => imgs.length === 1 && imgs[0].complete && imgs[0].naturalWidth > 0), 'economy preview did not load');
  await editor.screenshot({ path: output + '/media-editor.png', fullPage: true });
  await editor.close();
  checkpoint = 'room readiness and image';
  const rules = { preset: 'custom', selection_seconds: 120, answer_seconds: 120, special_seconds: 120, wager_seconds: 120, final_seconds: 120, penalties: true, buzzer_mode: 'first', window_ms: 200, false_start: false, false_start_seconds: 2, no_risk_multiplier: 2 };
  const hostCredentials = await request(ownerContext, 'POST', '/rooms', { host_name: 'Ведущий', pack_id: draft.card.id, revision: draft.card.revision, rules });
  const host = await newClient(hostCredentials, 'Ведущий', ownerContext), players = [];
  for (const name of ['Мобильный игрок', 'Второй игрок']) {
    const c = await request(ownerContext, 'POST', `/rooms/${hostCredentials.room_id}/participants`, { name, role: 'player', invite_token: hostCredentials.invite_token });
    players.push(await newClient(c, name));
  }
  let slowSeen = false, failNextMedia = false;
  const slow = new Promise(resolve => { releaseSlow = resolve; });
  await players[1].page.route('**/api/rooms/*/media/*', async route => {
    if (!slowSeen) { slowSeen = true; await slow; }
    if (failNextMedia) { failNextMedia = false; await route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }); return; }
    await route.continue();
  });
  await button(host, 'Начать раунд'); await stage(host, 'select_question');
  await button(host, 'Подготовленные файлы, 100 очков');
  await stage(host, 'prepare_content');
  await until(() => slowSeen, 'player never fetched current file');
  assert.equal(game(host).stage.kind, 'prepare_content', 'game ignored slow player');
  for (const p of players) assert.equal(game(p).question.answer, undefined);
  await new Promise(resolve => setTimeout(resolve, 1100)); // Cross a ticket expiry-second boundary while file loading is delayed.
  releaseSlow();
  await stage(host, 'present_question');
  for (const p of players) await until(async () => (await p.page.locator('.media-player img').isVisible()) && (await p.page.locator('.media-player img').evaluate(img => img.complete && img.naturalWidth > 0)), 'image not shown');
  assert.equal(await players[0].page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  const imageID = game(host).question.media.id;
  for (const p of players) {
    assert.equal(p.ticketRequests.filter(r => r.media_id === imageID).length, 1, 'prepared file ticket was refetched at common start');
    assert.equal(p.mediaRequests.filter(path => path.endsWith('/' + imageID)).length, 1, 'prepared image was downloaded again at common start');
  }
  await players[0].page.screenshot({ path: output + '/media-player-mobile.png', fullPage: true });
  checkpoint = 'media error, hidden tab and explicit wait';
  failNextMedia = true;
  await button(host, 'Продолжить →');
  await until(() => game(host).question.media?.kind === 'audio' && game(host).readiness?.some(r => r.id === players[1].credentials.participant_id && r.status === 'error'), 'failed file not reported to host');
  await until(() => game(host).stage.expired, 'preparation did not expire');
  await button(host, 'Ждать ещё 10 секунд');
  await until(() => !game(host).stage.expired, 'host wait did not restart preparation');
  await players[1].page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await until(() => game(host).readiness?.some(r => r.id === players[1].credentials.participant_id && r.status === 'hidden'), 'hidden tab not reported');
  await players[1].page.evaluate(() => { delete document.visibilityState; document.dispatchEvent(new Event('visibilitychange')); });
  const retry = players[1].page.getByRole('button', { name: 'Повторить загрузку медиа', exact: true });
  if (await retry.count()) await retry.click();
  checkpoint = 'audio synchronized start and pause';
  await until(() => game(host).question.media?.kind === 'audio' && game(host).stage.kind === 'present_question', 'audio fragment not selected');
  for (const p of players) await playing(p, 'audio');
  const positions = await Promise.all(players.map(p => p.page.locator('.media-player audio').evaluate(el => el.currentTime)));
  assert.ok(Math.abs(positions[0] - positions[1]) < 1.25, 'audio clients are not synchronized');
  await button(host, 'Пауза Ⅱ'); await until(() => players.every(p => p.state.phase === 'paused'), 'pause not delivered');
  await until(() => players[0].page.locator('.media-player audio').evaluate(el => el.paused), 'audio not paused');
  const paused = await players[0].page.locator('.media-player audio').evaluate(el => el.currentTime);
  await new Promise(resolve => setTimeout(resolve, 350));
  assert.ok(Math.abs(await players[0].page.locator('.media-player audio').evaluate(el => el.currentTime) - paused) < .15, 'paused media moved');
  await button(host, 'Продолжить ▶'); await until(() => players[0].state.phase !== 'paused', 'resume not delivered');
  await playing(players[0], 'audio');
  checkpoint = 'video and reconnect';
  await button(host, 'Продолжить →');
  await until(() => game(host).question.media?.kind === 'video' && game(host).stage.kind === 'present_question', 'video did not start');
  for (const p of players) await playing(p, 'video');
  await new Promise(resolve => setTimeout(resolve, 700));
  await players[0].page.reload();
  await players[0].page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
  await playing(players[0], 'video');
  const current = await players[0].page.locator('.media-player video').evaluate(el => el.currentTime);
  assert.ok(current > .3, 'reconnect restarted video from zero');
  for (const p of players) assert.equal(game(p).question.answer, undefined, 'answer leaked before reveal');
  await host.page.screenshot({ path: output + '/media-host.png', fullPage: true });
  checkpoint = 'answer media reveal and revocation';
  await button(host, 'Открыть кнопку'); await stage(host, 'buzz_open');
  for (const p of players) {
    await stage(p, 'buzz_open');
    const stop = game(p).question.media.stopped_position_ms;
    assert.ok(Number.isFinite(stop) && stop > 0 && stop < 8000, 'manual stop position missing');
    await until(() => p.page.locator('.media-player video').evaluate((el, seconds) => el.paused && Math.abs(el.currentTime - seconds) < .15, stop / 1000), 'last video kept playing after manual stop');
  }
  await players[0].page.reload();
  await players[0].page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
  await until(() => players[0].page.locator('.media-player video').evaluateAll(els => els.length === 1 && els[0].readyState >= 2 && els[0].paused), 'stopped video resumed on reconnect');
  await button(host, 'Раскрыть ответ');
  await until(() => game(host).question.media?.kind === 'image' && game(host).question.media.id !== imageID, 'answer media not revealed');
  await stage(host, 'present_answer');
  const answerID = game(host).question.media.id;
  for (const p of players) {
    assert.ok(p.mediaRequests.some(path => path.endsWith('/' + answerID)));
    const beforeReveal = p.wire.filter(s => s.gameplay?.question?.instance_id === game(host).question.instance_id && s.gameplay.question.answer === undefined);
    assert.ok(beforeReveal.every(s => s.gameplay.question.media?.id !== answerID), 'answer media was projected before reveal');
  }
  await button(host, 'Следующий фрагмент ответа'); await stage(host, 'resolved');
  for (const [kind, price] of [['audio', 200], ['video', 300]]) {
    checkpoint = `${kind} explicit duration stops last fragment`;
    const timed = draft.document.rounds[0].themes[0].questions.find(q => q.price === price);
    await request(ownerContext, 'POST', `/rooms/${hostCredentials.room_id}/select`, { question_id: timed.id, command_id: randomUUID(), expected_seq: host.state.seq }, hostCredentials.token);
    await stage(host, 'present_question');
    for (const p of players) await playing(p, kind);
    await stage(host, 'buzz_open');
    for (const p of players) {
      await stage(p, 'buzz_open');
      assert.equal(game(p).question.media.playback_end_position_ms, 2000);
      assert.equal(game(p).question.media.stopped_position_ms, 2000);
      await until(() => p.page.locator(`.media-player ${kind}`).evaluate(el => el.paused && Math.abs(el.currentTime - 2) < .15), 'explicit duration did not stop native playback');
    }
    await button(host, 'Раскрыть ответ'); await stage(host, 'resolved');
  }
  const original = await ownerContext.request.get(`${base}/api/drafts/${draft.card.id}/assets/${asset(sound.name).key}?revision=${draft.card.revision}`);
  assert.equal(original.status(), 200); assert.deepEqual(await original.body(), sound.data, 'original audio changed');
  const archive = await ownerContext.request.get(`${base}/api/drafts/${draft.card.id}/export?revision=${draft.card.revision}`);
  assert.equal(archive.status(), 200);
  const archivePath = join(fixtureDir, 'media-export.siq');
  await writeFile(archivePath, await archive.body());
  await run('python3', ['-c', 'import sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); assert z.read(sys.argv[2]) == open(sys.argv[3],"rb").read()', archivePath, asset(sound.name).name, sound.path], { timeout: 10000 });
  assert.equal(pageErrors.length, 0, JSON.stringify(pageErrors));
  console.log(JSON.stringify({ media_upload: true, editor_upload: true, unsaved_edit_preserved: true, prepared_editor: true, slow_readiness: true, media_error_retry: true, hidden_readiness: true, explicit_wait: true, image: true, audio: true, video: true, synchronized: true, no_duplicate_preload: true, pause: true, reconnect: true, manual_playback_stop: true, timed_audio_video_stop: true, stopped_reconnect: true, answer_private: true, original_preserved: true, siq_original_preserved: true, mobile_overflow: false, page_errors: 0 }));
} catch (err) {
  if (ownerContext) for (const [index, p] of ownerContext.pages().entries()) await p.screenshot({ path: `${output}/media-owner-failure-${index}.png`, fullPage: true }).catch(() => {});
  for (const c of clients) await c.page.screenshot({ path: `${output}/media-failure-${c.credentials.participant_id}.png`, fullPage: true }).catch(() => {});
  throw new Error(`${checkpoint}: ${err.message}`, { cause: err });
} finally {
  releaseSlow();
  if (registered) await request(ownerContext, 'DELETE', '/account/me', { password }).catch(() => {});
  for (const context of contexts) await context.close().catch(() => {});
  await browser.close();
  await rm(fixtureDir, { recursive: true });
}
