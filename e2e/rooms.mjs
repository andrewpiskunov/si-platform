import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const base = process.env.BASE_URL || 'http://localhost:5173';
const output = process.env.ARTIFACT_DIR || '../.local/browser';
const root = fileURLToPath(new URL('..', import.meta.url));
const browser = await chromium.launch();
const contexts = [], clients = [], sockets = [], created = [];
const errors = [], voiceRequests = [], report = {};
const password = randomBytes(20).toString('hex');
const username = 'rooms_' + randomBytes(6).toString('hex');
const roomTitle = 'Открытый вечер ' + username + 'W'.repeat(20);
const voiceURL = 'https://voice.example/PRIVATE-' + randomUUID();
let checkpoint = 'setup', registered = false, host;
await mkdir(output, { recursive: true });
async function context() {
  const result = await browser.newContext(); contexts.push(result);
  result.on('request', request => { if (request.url().startsWith('https://voice.example/')) voiceRequests.push(request.url()); });
  return result;
}
const owner = await context();
async function api(method, path, data, token, who = owner, expected = 200) {
  const response = await who.request.fetch(base + '/api' + path, { method, data, ...(token ? { headers: { Authorization: 'Bearer ' + token } } : {}) });
  assert.equal(response.status(), expected, `${method} ${path}: ${await response.text()}`);
  return response.status() === 204 ? null : response.json();
}
async function until(fn, label) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(checkpoint + ': ' + label);
}
async function page(who) {
  const page = await who.newPage(), client = { page, state: null, snapshots: 0 };
  clients.push(client);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    const Native = WebSocket;
    window.__roomTestSockets = [];
    window.WebSocket = class extends Native { constructor(...args) { super(...args); window.__roomTestSockets.push(this); } };
  });
  page.on('websocket', socket => socket.on('framereceived', ({ payload }) => {
    try { const data = JSON.parse(payload.toString()); if (data.state) client.state = data.state; if (data.type === 'snapshot') client.snapshots++; } catch {}
  }));
  return client;
}
async function online(client) {
  await until(() => client.state?.gameplay, 'missing room snapshot');
  await client.page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
}
async function openDetails(page, summary) {
  const details = page.locator('details').filter({ has: page.locator('summary').filter({ hasText: summary }) }).first();
  await details.waitFor();
  if (!(await details.evaluate(el => el.open))) await details.locator('summary').first().click();
  return details;
}
async function native(creds) {
  const socket = new WebSocket(base.replace(/^http/, 'ws') + '/api/rooms/' + creds.room_id + '/ws');
  const client = { socket, state: null, results: new Map() }; sockets.push(client);
  socket.onopen = () => socket.send(JSON.stringify({ v: 1, type: 'authenticate', token: creds.token }));
  socket.onmessage = event => { const message = JSON.parse(event.data); if (message.state) client.state = message.state; if (message.command_id) client.results.set(message.command_id, message); };
  await until(() => client.state, 'native room authentication');
  return client;
}
async function command(client, type, data = {}) {
  const id = randomUUID();
  client.socket.send(JSON.stringify({ v: 2, id, type, expected_seq: client.state.seq, data }));
  await until(() => client.results.has(id), type + ' result');
  const result = client.results.get(id); assert.equal(result.code, 'ok', type + ': ' + result.code);
  return result;
}
async function overflow(client, filename) {
  for (const width of [390, 820, 1440]) {
    await client.page.setViewportSize({ width, height: 900 });
    await until(() => client.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${filename} horizontal overflow ${width}`).catch(async error => {
      console.error(JSON.stringify(await client.page.evaluate(() => [...document.querySelectorAll('body *')].filter(el => el.getBoundingClientRect().right > innerWidth + 1).slice(0, 8).map(el => ({ tag: el.tagName, class: el.className, text: el.textContent.slice(0, 90), right: el.getBoundingClientRect().right })))));
      throw error;
    });
    if (width === 390) await client.page.screenshot({ path: `${output}/${filename}-mobile.png`, fullPage: true });
  }
}
async function joinFromList(name, role) {
  const who = await context(), client = await page(who);
  await client.page.goto(base + '/rooms');
  const card = client.page.locator('article').filter({ has: client.page.getByRole('heading', { name: roomTitle, exact: true }) });
  await card.getByRole('link', { name: role === 'player' ? 'Войти игроком' : 'Войти зрителем', exact: true }).click();
  assert.equal(new URL(client.page.url()).hash, '');
  await client.page.getByLabel('Ваше имя', { exact: true }).fill(name);
  assert.equal(await client.page.getByRole('combobox', { name: /^Роль/ }).inputValue(), role);
  await client.page.getByRole('button', { name: 'Войти в комнату →', exact: true }).click();
  await online(client);
  assert.equal(client.state.participants.find(p => p.id === client.state.you).role, role);
  return client;
}
try {
  await api('POST', '/account/register', { username, password, display_name: 'Ведущий проверки' }, null, owner, 201);
  registered = true;
  // Exposes the existing moderator navigation as well as normal guest navigation.
  assert.ok(process.env.DATABASE_URL, 'DATABASE_URL for local preview is required');
  await promisify(execFile)(process.env.GO || 'go', ['run', './cmd/admin', 'role', username, 'moderator'], { cwd: root, env: process.env, timeout: 60000 });
  checkpoint = 'creation before hydration';
  const staticContext = await browser.newContext({ javaScriptEnabled: false });
  contexts.push(staticContext);
  const staticPage = await staticContext.newPage();
  await staticPage.goto(base + '/studio');
  const staticForm = await openDetails(staticPage, /^Быстро попробовать игру$/);
  for (const control of await staticForm.locator('input, select, button').all())
    assert.equal(await control.isDisabled(), true, 'creation input enabled before its handlers are ready');
  await staticPage.close();
  checkpoint = 'create UI';
  const hp = await page(owner);
  await hp.page.goto(base + '/studio');
  await hp.page.waitForLoadState('networkidle');
  const create = await openDetails(hp.page, /^Быстро попробовать игру$/);
  await create.getByLabel('Название комнаты', { exact: true }).fill(roomTitle);
  await create.getByRole('combobox', { name: /^Доступ в комнату/ }).waitFor({ timeout: 8000 });
  assert.equal(await create.getByRole('combobox', { name: /^Доступ в комнату/ }).inputValue(), 'private');
  await create.getByRole('combobox', { name: /^Доступ в комнату/ }).selectOption('public');
  await create.getByText('Комната появится в списке. Любой посетитель сможет занять доступное место.', { exact: true }).waitFor({ timeout: 5000 });
  await create.getByRole('textbox', { name: /^Голосовая ссылка \(HTTPS\)/ }).fill(voiceURL);
  const createdResponse = hp.page.waitForResponse(r => r.url() === base + '/api/rooms' && r.request().method() === 'POST');
  await create.getByRole('button', { name: 'Создать комнату →', exact: true }).click();
  const creationResponse = await createdResponse;
  host = await creationResponse.json(); created.push(host);
  assert.deepEqual(creationResponse.request().postDataJSON().settings, { visibility: 'public', voice_url: voiceURL }, 'creation request settings');
  await online(hp);
  assert.equal(hp.state.settings.visibility, 'public');
  assert.equal(hp.state.settings.voice_url, voiceURL);
  const oldInvite = host.invite_token;
  assert.ok(oldInvite);
  report.creation = 'hydration-safe form, explicit public UI, private default';

  checkpoint = 'public list and guest roles';
  const player = await joinFromList('Игрок без аккаунта', 'player');
  const spectator = await joinFromList('Зритель без аккаунта', 'spectator');
  for (const client of [hp, player, spectator]) {
    await client.page.getByText('Текущие правила · версия 1', { exact: true }).waitFor();
    assert.equal(await client.page.getByText(/^Правила изменены: версия/).count(), 0, 'first view announced a change');
    const voice = client.page.locator(`a[href="${voiceURL}"]`);
    assert.equal(await voice.getAttribute('target'), '_blank');
    const rel = await voice.getAttribute('rel'); assert.ok(rel.includes('noopener') && rel.includes('noreferrer'));
    assert.match(await voice.innerText(), /voice\.example/);
  }
  const listing = await api('GET', '/rooms');
  assert.ok(listing.items.some(item => item.room_id === host.room_id && item.players === 1 && item.spectators === 1));
  assert.ok(!JSON.stringify(listing).includes('PRIVATE-') && !JSON.stringify(listing).includes('Игрок без аккаунта'));
  report.publicGuests = 'player and spectator joined without account or invitation';

  checkpoint = 'rules notice and dirty form';
  const rulesForm = await openDetails(hp.page, /^Правила игры$/);
  await rulesForm.getByLabel('Ответ после кнопки, секунд', { exact: true }).fill('17');
  await rulesForm.getByLabel('Ответ после кнопки, секунд', { exact: true }).blur();
  const remote = await native(host);
  const changed = { ...remote.state.gameplay.rules, preset: 'custom', answer_seconds: 23, buzzer_mode: 'random', window_ms: 250 };
  await command(remote, 'set_rules', { rules: changed });
  for (const client of [hp, player, spectator]) await client.page.getByText(/^Правила изменены: версия 2\./).waitFor();
  assert.equal(await rulesForm.getByLabel('Ответ после кнопки, секунд', { exact: true }).inputValue(), '17', 'snapshot erased dirty rules');
  await rulesForm.getByRole('button', { name: 'Сбросить изменения правил', exact: true }).click();
  assert.equal(await rulesForm.getByLabel('Ответ после кнопки, секунд', { exact: true }).inputValue(), '23');
  // Dismiss before reconnect, otherwise a persistent existing notice cannot prove no repeat.
  await player.page.getByRole('button', { name: 'Скрыть уведомление о правилах', exact: true }).click();
  const snapshots = player.snapshots;
  await player.page.evaluate(() => window.__roomTestSockets.forEach(socket => socket.close()));
  await until(() => player.snapshots > snapshots, 'real websocket did not reconnect');
  assert.equal(await player.page.getByText(/^Правила изменены: версия/).count(), 0, 'same version notified again');
  // This tab misses the next change entirely, then restores its last-seen version on return.
  await player.page.goto(base + '/rooms');
  await command(remote, 'set_rules', { rules: { ...changed, answer_seconds: 29 } });
  await player.page.goto(base + '/rooms/' + host.room_id);
  await online(player);
  await player.page.getByText(/^Правила изменены: версия 3\./).waitFor();
  const journal = await api('GET', '/rooms/' + host.room_id + '/results', null, host.token);
  const ruleEvents = journal.items.filter(item => item.rules_change);
  assert.deepEqual(ruleEvents.map(item => item.rules_change.version), [2, 3]);
  report.rules = 'all roles, dirty form preserved/reset, real reconnect dedup, missed version, journal';

  checkpoint = 'privacy change and invitations';
  const settings = await openDetails(hp.page, /^Настройки комнаты$/);
  await settings.getByRole('textbox', { name: /^Голосовая ссылка \(HTTPS\)/ }).fill('https://voice.example/UNSAVED');
  await settings.getByRole('textbox', { name: /^Голосовая ссылка \(HTTPS\)/ }).blur();
  await command(remote, 'set_room_settings', { settings: { visibility: 'public', voice_url: 'https://voice.example/REMOTE' } });
  await until(() => hp.state.settings.voice_url.endsWith('/REMOTE'), 'settings snapshot');
  assert.equal(await settings.getByRole('textbox', { name: /^Голосовая ссылка \(HTTPS\)/ }).inputValue(), 'https://voice.example/UNSAVED');
  await settings.getByRole('button', { name: 'Сбросить изменения настроек', exact: true }).click();
  await settings.getByRole('combobox', { name: /^Доступ в комнату/ }).selectOption('private');
  await settings.getByRole('button', { name: 'Сохранить настройки комнаты', exact: true }).click();
  await until(() => hp.state.settings.visibility === 'private' && player.state.settings.visibility === 'private', 'private state not delivered');
  assert.equal(new URL(hp.page.url()).hash, '', 'invalidated invitation left in address');
  await hp.page.getByRole('button', { name: 'Создать приглашение', exact: true }).waitFor();
  const closed = await api('GET', '/rooms'); assert.ok(!closed.items.some(item => item.room_id === host.room_id));
  await api('POST', '/rooms/' + host.room_id + '/participants', { name: 'Stale list', role: 'player' }, null, owner, 401);
  await api('POST', '/rooms/' + host.room_id + '/participants', { name: 'Old invitation', role: 'spectator', invite_token: oldInvite }, null, owner, 401);
  assert.ok(player.state.participants.some(item => item.id === player.state.you));
  await hp.page.getByRole('button', { name: 'Создать приглашение', exact: true }).click();
  await until(() => new URL(hp.page.url()).hash.includes('invite='), 'new invitation URL');
  const invite = new URLSearchParams(new URL(hp.page.url()).hash.slice(1)).get('invite');
  assert.ok(invite && invite !== oldInvite);
  const newcomer = await page(await context());
  await newcomer.page.goto(`${base}/rooms/${host.room_id}#invite=${invite}`);
  await newcomer.page.getByLabel('Ваше имя', { exact: true }).fill('По новой ссылке');
  await newcomer.page.getByRole('button', { name: 'Войти в комнату →', exact: true }).click();
  await online(newcomer);
  report.privacy = 'atomic removal from list, stale entry/link denied, admitted seats retained, new invite joins';

  checkpoint = 'public list pagination and responsive layout';
  for (let i = 0; i < 2; i++) created.push(await api('POST', '/rooms', { host_name: 'List host', title: `Pagination ${username} ${i}`, settings: { visibility: 'public', voice_url: '' } }, null, owner, 201));
  const list = await page(owner);
  await list.page.goto(base + '/rooms?limit=1');
  await list.page.getByRole('heading', { name: 'Публичные комнаты', exact: true }).waitFor();
  assert.equal(await list.page.locator('article.public-room').count(), 1);
  const firstRoomTitle = await list.page.locator('article.public-room h2').innerText();
  await list.page.getByRole('link', { name: 'Следующие комнаты →', exact: true }).click();
  await until(() => new URL(list.page.url()).searchParams.has('after'), 'cursor not navigated');
  assert.equal(new URL(list.page.url()).searchParams.get('limit'), '1');
  assert.notEqual(await list.page.locator('article.public-room h2').innerText(), firstRoomTitle);
  await list.page.getByRole('navigation', { name: 'Основная навигация' }).getByRole('link', { name: 'Модерация', exact: true }).waitFor();
  const beforeRefresh = await list.page.locator('article.public-room h2').allTextContents();
  const cursorURL = list.page.url();
  const abortRefresh = route => route.abort('connectionfailed');
  await list.page.route(/\/(?:api\/rooms|rooms\/__data\.json)\?/, abortRefresh);
  await list.page.getByRole('button', { name: 'Обновить список комнат', exact: true }).click();
  await list.page.getByRole('button', { name: 'Повторить загрузку комнат', exact: true }).waitFor();
  assert.deepEqual(await list.page.locator('article.public-room h2').allTextContents(), beforeRefresh);
  assert.equal(list.page.url(), cursorURL);
  await list.page.unroute(/\/(?:api\/rooms|rooms\/__data\.json)\?/, abortRefresh);
  await list.page.getByRole('button', { name: 'Повторить загрузку комнат', exact: true }).click();
  await until(async () => await list.page.getByRole('alert').count() === 0, 'list retry failed');
  assert.deepEqual(await list.page.locator('article.public-room h2').allTextContents(), beforeRefresh);
  await overflow(list, 'rooms-list');
  await overflow(hp, 'rooms-host');
  await overflow(player, 'rooms-player');
  await list.page.goto(base + '/rooms?after=invalid');
  await list.page.getByRole('alert').filter({ hasText: 'Некорректный адрес' }).waitFor();
  await list.page.getByRole('link', { name: 'К началу списка', exact: true }).click();
  await list.page.getByRole('heading', { name: 'Публичные комнаты', exact: true }).waitFor();
  assert.deepEqual(voiceRequests, [], 'voice URL loaded without a user click');
  assert.deepEqual(errors, [], 'browser page errors');
  report.layout = '390/820/1440 host/player/list, moderator navigation, cursor and error recovery';
  report.voice = 'external safe link only, zero outbound voice requests';
  await writeFile(output + '/rooms-report.json', JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ result: 'PASS', ...report }, null, 2));
} catch (error) {
  for (let i = 0; i < clients.length; i++) await clients[i].page.screenshot({ path: `${output}/rooms-failure-${i}.png`, fullPage: true }).catch(() => {});
  console.error(JSON.stringify({ checkpoint, errors, states: clients.map(client => ({ phase: client.state?.phase, settings: client.state?.settings, version: client.state?.gameplay?.rules_version })) }));
  throw error;
} finally {
  // Remove only synthetic public games from discovery before account cleanup.
  for (const client of clients) await client.page.close().catch(() => {});
  for (const client of sockets) client.socket.close();
  for (const creds of created) {
    try { const client = await native(creds); if (client.state.phase !== 'finished') await command(client, 'finish'); client.socket.close(); } catch (error) { console.error('Synthetic room cleanup: ' + error.message); }
  }
  if (registered) { const result = await owner.request.delete(base + '/api/account/me', { data: { password } }); if (!result.ok()) console.error('Synthetic account cleanup: ' + result.status()); }
  for (const client of sockets) client.socket.close();
  await Promise.all(contexts.map(context => context.close()));
  await browser.close();
}
