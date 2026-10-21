import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';

const base = process.env.BASE_URL || 'http://localhost:5173';
const output = process.env.ARTIFACT_DIR || '../.local/browser';
const browser = await chromium.launch();
const contexts = [], clients = [], sockets = [], created = [], errors = [];
const report = {}, password = randomBytes(20).toString('hex');
const username = 'membership_' + randomBytes(6).toString('hex');
let checkpoint = 'setup', registered = false;
await mkdir(output, { recursive: true });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function context(deniedStorage = false) {
  const who = await browser.newContext(); contexts.push(who);
  if (deniedStorage) await who.addInitScript(() => {
    for (const method of ['getItem', 'setItem', 'removeItem']) {
      const original = Storage.prototype[method];
      Storage.prototype[method] = function (...args) {
        if (this === window.sessionStorage) throw new DOMException('Storage denied by browser test', 'SecurityError');
        return original.apply(this, args);
      };
    }
  });
  return who;
}
const owner = await context(true);
async function api(method, path, data, token, who = owner, expected = 200) {
  const response = await who.request.fetch(base + '/api' + path, {
    method, data, ...(token ? { headers: { Authorization: 'Bearer ' + token } } : {})
  });
  assert.equal(response.status(), expected, `${method} ${path}: ${await response.text()}`);
  return response.status() === 204 ? null : response.json();
}
async function until(fn, label) {
  const end = Date.now() + 25000;
  while (Date.now() < end) { if (await fn()) return; await delay(40); }
  throw new Error(checkpoint + ': ' + label);
}
async function page(who) {
  const page = await who.newPage();
  const client = { page, who, state: null, credentials: null, socketCount: 0, joins: 0, sent: 0, wire: [] };
  clients.push(client);
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    if (/\/participants$/.test(request.url()) && request.method() === 'POST') client.joins++;
  });
  page.on('websocket', socket => {
    const url = new URL(socket.url());
    if (!url.pathname.startsWith('/api/rooms/')) return;
    client.socketCount++;
    if (url.searchParams.has('token')) errors.push('room credential in socket URL');
    socket.on('framesent', () => client.sent++);
    socket.on('framereceived', ({ payload }) => {
      try {
        const message = JSON.parse(payload.toString());
        if (message.state) { client.state = message.state; client.wire.push(message.state); }
      } catch {}
    });
  });
  return client;
}
async function online(client, id) {
  await until(() => client.state?.gameplay && (!id || client.state.you === id), 'missing gameplay snapshot');
  await client.page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
}
async function details(client, name) {
  const item = client.page.locator('details').filter({ has: client.page.locator('summary').filter({ hasText: name }) }).first();
  await item.waitFor();
  if (!(await item.evaluate(el => el.open))) await item.locator('summary').first().click();
  return item;
}
async function join(client, creds, name, role = 'spectator', navigate = true) {
  if (navigate) await client.page.goto(`${base}/rooms/${creds.room_id}#invite=${creds.invite_token || ''}`);
  await client.page.getByLabel('Ваше имя', { exact: true }).fill(name);
  await client.page.getByRole('combobox', { name: /^Роль/ }).selectOption(role);
  const response = client.page.waitForResponse(r => r.url().endsWith(`/rooms/${creds.room_id}/participants`) && r.request().method() === 'POST');
  await client.page.getByRole('button', { name: 'Войти в комнату →', exact: true }).click();
  const result = await response;
  assert.equal(result.status(), 201, await result.text());
  client.credentials = await result.json();
  await online(client, client.credentials.participant_id);
  return client;
}
async function native(creds) {
  const socket = new WebSocket(base.replace(/^http/, 'ws') + '/api/rooms/' + creds.room_id + '/ws');
  const client = { socket, state: null, results: new Map() }; sockets.push(client);
  socket.onopen = () => socket.send(JSON.stringify({ v: 1, type: 'authenticate', token: creds.token }));
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.state) client.state = message.state;
    if (message.command_id) client.results.set(message.command_id, message);
  };
  await until(() => client.state, 'native authentication');
  return client;
}
async function command(client, type, data = {}, expected = 'ok') {
  const id = randomUUID();
  client.socket.send(JSON.stringify({ v: 2, id, type, expected_seq: client.state.seq, data }));
  await until(() => client.results.has(id), type + ' result');
  const result = client.results.get(id);
  assert.equal(result.code, expected, `${type}: ${result.code}`);
  return result;
}
async function removeUI(host, target, mode) {
  await until(() => host.state?.participants.some(p => p.id === target && !p.removed), 'host missing target');
  const controls = await details(host, /^Управление участниками$/);
  await controls.getByRole('combobox', { name: /^Участник для удаления/ }).selectOption(target);
  await controls.getByRole('button', { name: mode === 'kick' ? 'Удалить участника' : 'Запретить вход этой сессии', exact: true }).click();
  // Opening confirmation must not change membership.
  assert.ok(host.state.participants.some(p => p.id === target && !p.removed));
  await controls.getByRole('group', { name: 'Подтверждение удаления' }).waitFor();
  await controls.getByRole('button', { name: mode === 'kick' ? 'Подтвердить удаление' : 'Подтвердить запрет сессии', exact: true }).click();
}
async function revoked(client) {
  await client.page.getByRole('heading', { name: 'Доступ к комнате прекращён', exact: true }).waitFor();
  assert.equal(await client.page.locator('audio,video,.theme-background').count(), 0, 'revocation retained media');
  assert.equal(await client.page.evaluate(room => sessionStorage.getItem('room:' + room), client.credentials.room_id), null);
  const previous = { sockets: client.socketCount, joins: client.joins, sent: client.sent };
  await delay(1600);
  assert.deepEqual({ sockets: client.socketCount, joins: client.joins, sent: client.sent }, previous, 'revocation reconnected, replayed or automatically rejoined');
}
async function overflow(client, name) {
  for (const width of [390, 820, 1440]) {
    await client.page.setViewportSize({ width, height: 900 });
    await until(() => client.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${name} overflow ${width}`);
    await client.page.screenshot({ path: `${output}/membership-${name}-${width}.png`, fullPage: true });
  }
}
try {
  await api('POST', '/account/register', { username, password, display_name: 'Проверка состава' }, null, owner, 201);
  registered = true;
  checkpoint = 'host creation with denied sessionStorage';
  const host = await page(owner);
  await host.page.goto(base + '/studio');
  const create = await details(host, /^Быстро попробовать игру$/);
  await create.getByLabel('Название комнаты', { exact: true }).fill('Состав ' + username);
  await create.getByRole('combobox', { name: /^Доступ в комнату/ }).selectOption('public');
  const creation = host.page.waitForResponse(r => r.url() === base + '/api/rooms' && r.request().method() === 'POST');
  await create.getByRole('button', { name: 'Создать комнату →', exact: true }).click();
  const response = await creation;
  assert.equal(response.status(), 201, await response.text());
  const creds = await response.json(); created.push(creds); host.credentials = creds;
  await online(host, creds.participant_id);
  assert.equal(host.state.participants.find(p => p.id === host.state.you).role, 'host');
  assert.equal(await host.page.evaluate(() => { try { sessionStorage.getItem('x'); return false; } catch { return true; } }), true);
  assert.ok(!host.page.url().includes(creds.token));
  const remote = await native(creds);
  report.deniedStorageHost = 'newly created host connected through shared in-memory credential despite denied get/set/remove';

  checkpoint = 'kick and explicit zero-score rejoin';
  const guests = await context();
  const first = await join(await page(guests), creds, 'Игрок для удаления', 'player');
  const old = first.credentials;
  await until(() => remote.state.participants.some(p => p.id === old.participant_id), 'remote membership');
  await command(remote, 'adjust_score', { target_id: old.participant_id, delta: 75, reason: 'Синтетическая проверка истории' });
  await until(() => host.state.participants.find(p => p.id === old.participant_id)?.score === 75, 'score before kick');
  await removeUI(host, old.participant_id, 'kick');
  await revoked(first);
  await api('GET', `/rooms/${creds.room_id}/board`, undefined, old.token, guests, 401);
  await first.page.getByRole('button', { name: 'Войти заново по приглашению', exact: true }).click();
  await join(first, creds, 'Игрок вернулся', 'player', false);
  assert.notEqual(first.credentials.participant_id, old.participant_id);
  assert.equal(first.state.participants.find(p => p.id === first.state.you).score, 0);
  report.kick = 'explicit confirmation, terminal cleanup, no reconnect/replay/rejoin, stale bearer denied, new identity and zero score';

  checkpoint = 'ban group containing current host is atomic denial';
  const hostSibling = await join(await page(owner), creds, 'Вкладка ведущего');
  await removeUI(host, hostSibling.credentials.participant_id, 'ban_guest_session');
  await host.page.getByRole('alert').filter({ hasText: 'У вас нет доступа к этому действию.' }).waitFor();
  await api('GET', `/rooms/${creds.room_id}/board`, undefined, hostSibling.credentials.token);
  await online(hostSibling);
  await api('GET', `/rooms/${creds.room_id}/board`, undefined, creds.token);
  report.hostGroup = 'ban rejected atomically; host and sibling retain authorized access';

  checkpoint = 'same browser session ban evicts both tabs, other room unaffected';
  const second = await join(await page(guests), creds, 'Вторая вкладка сессии');
  const other = await api('POST', '/rooms', { host_name: 'Другой ведущий', title: 'Другая комната ' + username }, null, owner, 201);
  created.push(other);
  const elsewhere = await join(await page(guests), other, 'Эта комната остаётся');
  await removeUI(host, first.credentials.participant_id, 'ban_guest_session');
  await Promise.all([revoked(first), revoked(second)]);
  await api('GET', `/rooms/${other.room_id}/board`, undefined, elsewhere.credentials.token, guests);
  await online(elsewhere);
  await first.page.getByRole('button', { name: 'Войти заново по приглашению', exact: true }).click();
  await first.page.getByLabel('Ваше имя', { exact: true }).fill('Повтор запрещён');
  await first.page.getByRole('button', { name: 'Войти в комнату →', exact: true }).click();
  await first.page.getByRole('alert').filter({ hasText: 'Вход этой браузерной сессии в комнату запрещён ведущим.' }).waitFor();
  report.sessionBan = 'two tabs evicted, explicit re-entry blocked, another room in same browser unaffected';

  checkpoint = 'contextual stale invitation';
  const denied = await page(await context());
  await denied.page.goto(base + '/rooms/' + other.room_id);
  await denied.page.getByLabel('Ваше имя', { exact: true }).fill('Без приглашения');
  await denied.page.getByRole('button', { name: 'Войти в комнату →', exact: true }).click();
  await denied.page.getByRole('alert').filter({ hasText: 'Приглашение недействительно или устарело.' }).waitFor();
  assert.equal(await denied.page.getByRole('alert').filter({ hasText: 'Войдите в аккаунт' }).count(), 0);
  report.join401 = 'fresh invitation advice instead of account login';

  checkpoint = 'bounded snapshots and roster pagination';
  const rotating = await context();
  for (let index = 0; index < 55; index++) {
    const guest = await api('POST', `/rooms/${creds.room_id}/participants`, { name: `Архив ${index} ` + 'Я'.repeat(20), role: 'spectator' }, undefined, rotating, 201);
    await until(() => remote.state.participants.some(p => p.id === guest.participant_id), 'archive admission');
    await command(remote, 'remove_participant', { target_id: guest.participant_id, mode: 'kick' });
    await delay(150);
  }
  const roster = await api('GET', `/rooms/${creds.room_id}/roster?limit=50`, undefined, creds.token);
  assert.equal(roster.items.length, 50);
  assert.match(roster.next_after, /^[a-f0-9]{32}$/);
  const tail = await api('GET', `/rooms/${creds.room_id}/roster?limit=50&after=${roster.next_after}`, undefined, creds.token);
  const all = [...roster.items, ...tail.items];
  assert.ok(all.filter(p => p.removed).length >= 58);
  assert.equal(new Set(all.map(p => p.id)).size, all.length);
  assert.equal(all.find(p => p.id === old.participant_id).score, 75);
  assert.ok(Buffer.byteLength(JSON.stringify(remote.state)) < 65536);
  assert.ok(remote.state.participants.length < all.length, 'snapshot did not prune historical participants');
  const history = await details(host, /^История состава$/);
  await history.getByRole('button', { name: 'Загрузить состав', exact: true }).click();
  await history.getByRole('button', { name: 'Ещё участники', exact: true }).click();
  await until(async () => await history.locator('.score-item').count() === all.length, 'roster pagination UI');
  await history.getByRole('heading', { name: 'Удалённые участники', exact: true }).waitFor();
  await overflow(host, 'host-history');
  const results = await api('GET', `/rooms/${creds.room_id}/results?limit=100`, undefined, creds.token);
  assert.ok(results.items.some(event => event.removed_ids?.includes(old.participant_id)));
  assert.ok(!JSON.stringify({ roster: all, results, state: remote.state }).match(/guest_hash|si_guest|"token"|"raw_command"/));
  report.history = '55 repeated joins/kicks; bounded snapshot, two roster pages, historical score, safe removal events, responsive 390/820/1440';

  checkpoint = 'active player removal pauses and preserves question references';
  const playersContext = await context();
  const alpha = await join(await page(playersContext), creds, 'Оставшийся игрок', 'player');
  const beta = await join(await page(playersContext), creds, 'Игрок активного вопроса', 'player');
  const board = await api('GET', `/rooms/${creds.room_id}/board`, undefined, creds.token);
  const question = board.rounds.flatMap(round => round.themes.flatMap(theme => theme.questions)).find(q => !q.played);
  assert.ok(question);
  await until(() => remote.state.participants.some(p => p.id === beta.credentials.participant_id), 'second active player');
  const round = board.rounds.find(round => round.themes.some(theme => theme.questions.some(q => q.id === question.id)));
  const beforeRound = remote.state.seq;
  await api('POST', `/rooms/${creds.room_id}/round`, { round_id: round.id, command_id: randomUUID(), expected_seq: beforeRound }, creds.token);
  await until(() => remote.state.seq > beforeRound && remote.state.gameplay.round?.id === round.id, 'round start');
  await api('POST', `/rooms/${creds.room_id}/select`, { question_id: question.id, command_id: randomUUID(), expected_seq: remote.state.seq }, creds.token);
  await until(() => remote.state.gameplay.question, 'active question');
  const before = structuredClone(remote.state.gameplay);
  const target = [alpha, beta].find(p => p.credentials.participant_id === before.chooser_id) || beta;
  await removeUI(host, target.credentials.participant_id, 'kick');
  await revoked(target);
  await until(() => remote.state.phase === 'paused', 'removal did not pause active question');
  assert.equal(remote.state.gameplay.question.instance_id, before.question.instance_id);
  assert.equal(remote.state.gameplay.chooser_id, before.chooser_id, 'frozen chooser changed during active question');
  await host.page.getByText('Игра на паузе. Остаток времени сохранён.', { exact: true }).waitFor();
  await overflow(target, 'revoked');
  report.activeQuestion = 'player removal paused game and retained current instance and chooser';
  assert.deepEqual(errors, [], 'browser page errors');
  await writeFile(output + '/membership-report.json', JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ result: 'PASS', ...report }, null, 2));
} catch (error) {
  for (let index = 0; index < clients.length; index++) await clients[index].page.screenshot({ path: `${output}/membership-failure-${index}.png`, fullPage: true }).catch(() => {});
  console.error(JSON.stringify({ checkpoint, errors, states: clients.map(client => ({ phase: client.state?.phase, seq: client.state?.seq, sockets: client.socketCount, joins: client.joins })) }));
  throw error;
} finally {
  for (const client of clients) await client.page.close().catch(() => {});
  for (const client of sockets) client.socket.close();
  for (const creds of created) {
    try { const client = await native(creds); if (client.state.phase !== 'finished') await command(client, 'finish'); client.socket.close(); }
    catch (error) { console.error('Synthetic room cleanup: ' + error.message); }
  }
  if (registered) {
    const response = await owner.request.delete(base + '/api/account/me', { data: { password } });
    if (!response.ok()) console.error('Synthetic account cleanup: ' + response.status());
  }
  for (const client of sockets) client.socket.close();
  await Promise.all(contexts.map(who => who.close()));
  await browser.close();
}
