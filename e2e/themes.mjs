import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const base = process.env.BASE_URL || 'http://localhost:5173';
const output = process.env.ARTIFACT_DIR || fileURLToPath(new URL('../.local/browser', import.meta.url));
const root = fileURLToPath(new URL('..', import.meta.url));
const run = promisify(execFile);
await mkdir(output, { recursive: true });
const temporary = await mkdtemp(join(tmpdir(), 'si-theme-browser-'));
const browser = await chromium.launch();
const contexts = [], clients = [], sockets = [], pageErrors = [], externalRequests = [];
const suffix = randomBytes(8).toString('hex'), password = randomBytes(20).toString('hex');
let ownerContext, moderatorContext, registered = false, moderatorRegistered = false;
let checkpoint = 'setup', releaseSlow = () => {}, revocation = '', slowHeld = false;
const report = {};
function at(name) { checkpoint = name; console.log('[themes] ' + name); }

async function request(context, method, path, data, token) {
  const response = await context.request.fetch(base + '/api' + path, { method, data, ...(token ? { headers: { Authorization: `Bearer ${token}` } } : {}) });
  assert.ok(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`);
  return response.status() === 204 ? null : response.json();
}
async function until(predicate, message, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`${checkpoint}: ${message}`);
}
async function context(options = {}) {
  const result = await browser.newContext(options); contexts.push(result); return result;
}
function observe(page, name, credentials = null) {
  const client = { page, name, credentials, state: null, wire: [], refreshes: 0, assetRequests: [], assetResponses: [], appearances: [] };
  clients.push(client);
  page.on('pageerror', error => pageErrors.push({ client: name, message: error.message }));
  page.on('request', req => {
    const url = new URL(req.url());
    if (/^https?:$/.test(url.protocol) && url.origin !== new URL(base).origin) externalRequests.push({ client: name, url: url.href });
    if (/\/rooms\/[^/]+\/appearance\/assets\//.test(url.pathname)) {
      assert.equal(url.searchParams.has('token'), false, 'room token leaked into image URL');
      client.assetRequests.push({ url: url.pathname + url.search, quality: url.searchParams.get('quality') });
    }
    if (/\/rooms\/[^/]+\/appearance$/.test(url.pathname) && req.method() === 'GET') client.appearances.push(url.pathname);
  });
  page.on('response', response => {
    if (/\/rooms\/[^/]+\/appearance\/assets\//.test(response.url()) && response.ok()) client.assetResponses.push({ url: response.url(), bytes: Number(response.headers()['content-length'] || 0) });
  });
  page.on('websocket', socket => socket.on('framereceived', ({ payload }) => {
    try {
      const message = JSON.parse(payload.toString());
      if (message.type === 'appearance_refresh') client.refreshes++;
      if (message.state) {
        client.wire.push(message.state);
        if (!client.state || message.state.seq >= client.state.seq) client.state = message.state;
      }
    } catch { /* Ignore non-JSON transport frames. */ }
  }));
  return client;
}
async function browserPlayer(credentials, name, options = {}) {
  const c = await context(options), page = await c.newPage();
  // Keep room credentials in this tab only; independent tabs must not inherit them.
  await page.addInitScript(value => sessionStorage.setItem('room:' + value.room_id, JSON.stringify(value)), credentials);
  const client = observe(page, name, credentials);
  await page.goto(`${base}/rooms/${credentials.room_id}`);
  await until(() => client.state?.gameplay?.version === 2, `${name}: no snapshot`);
  await page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
  return client;
}
async function nativePlayer(credentials) {
  const url = new URL(`/api/rooms/${credentials.room_id}/ws`, base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(url), client = { credentials, state: null, errors: [] };
  sockets.push(socket);
  socket.addEventListener('open', () => socket.send(JSON.stringify({ v: 1, type: 'authenticate', token: credentials.token })));
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.state) client.state = message.state;
    if (message.type === 'error') client.errors.push(message.code);
  });
  await until(() => client.state?.gameplay?.version === 2, 'native participant did not connect');
  return client;
}
async function shell(client, theme) {
  await until(async () => await client.page.locator('.app-shell').getAttribute('data-theme') === theme, `${client.name}: ${theme} theme did not apply`);
}
async function decorLoaded(page, selector = '.app-shell > img, .app-shell .brand-mark img') {
  await until(() => page.locator(selector).evaluateAll(images => images.length > 0 && images.every(image => image.complete && image.naturalWidth > 0 && image.src.startsWith('blob:'))), 'prepared theme images did not load');
}
async function details(page, selector) {
  const target = page.locator(selector);
  if (!(await target.evaluate(el => el.open))) await target.locator('summary').click();
  return target;
}
async function preference(client, label, checked) {
  const panel = await details(client.page, '.theme-preferences');
  await panel.getByLabel(label, { exact: true }).setChecked(checked);
}
async function click(client, name) { await client.page.getByRole('button', { name, exact: true }).click(); }
async function stage(client, kind) {
  await until(() => client.state?.gameplay?.stage.kind === kind, `${client.name}: stage ${kind}, got ${client.state?.gameplay?.stage.kind}`);
}
async function applyAppearance(host, value) {
  const panel = await details(host.page, '.theme-apply');
  if (value.theme_id) await panel.getByLabel('Моя тема', { exact: true }).selectOption(value.theme_id);
  else await panel.getByRole('button', { name: { classic: 'Классика', pirate: 'Пираты', anime: 'Аниме' }[value.builtin], exact: true }).click();
  await panel.getByRole('button', { name: 'Применить оформление', exact: true }).click();
  await until(() => value.theme_id ? host.state?.appearance?.theme_id === value.theme_id : host.state?.appearance?.builtin === value.builtin, 'appearance command did not arrive');
}
async function uploadUI(page, data, name) {
  await page.getByLabel('ZIP темы', { exact: true }).setInputFiles({ name, mimeType: 'application/zip', buffer: data });
  await page.getByRole('button', { name: 'Загрузить и проверить тему', exact: true }).click();
  await page.getByText('Готова к сохранению', { exact: false }).waitFor({ timeout: 30000 });
}
async function saveUI(page, target = '') {
  await page.getByLabel('Сохранить тему как', { exact: true }).selectOption(target);
  const saved = page.waitForResponse(response => response.request().method() === 'POST' && /\/api\/themes(?:\/[a-f0-9]{32}\/versions)?$/.test(new URL(response.url()).pathname));
  await page.getByRole('button', { name: 'Сохранить тему', exact: true }).click();
  const response = await saved;
  assert.equal(response.status(), 201, await response.text());
  return response.json();
}
async function changedArchive(source, name) {
  const input = join(temporary, 'source.zip'), destination = join(temporary, 'changed.zip');
  await writeFile(input, source);
  await run('python3', ['-c', 'import json,sys,zipfile\nwith zipfile.ZipFile(sys.argv[1]) as src, zipfile.ZipFile(sys.argv[2],"w",zipfile.ZIP_DEFLATED) as dst:\n for item in src.infolist():\n  data=src.read(item.filename)\n  if item.filename=="theme.json":\n   manifest=json.loads(data); manifest["name"]=sys.argv[3]; data=json.dumps(manifest,ensure_ascii=False).encode()\n  dst.writestr(item.filename,data)', input, destination, name], { timeout: 10000 });
  return readFile(destination);
}

try {
  ownerContext = await context();
  await request(ownerContext, 'POST', '/account/register', { username: 'theme_' + suffix, password, display_name: 'Автор оформления' });
  registered = true;
  const ownerPage = await ownerContext.newPage(), host = observe(ownerPage, 'host');
  at('invalid upload diagnostics and downloadable example');
  await ownerPage.goto(base + '/themes');
  await ownerPage.getByText('У вас пока нет своих тем. Начните с примера ZIP.', { exact: true }).waitFor();
  await ownerPage.getByLabel('ZIP темы', { exact: true }).setInputFiles({ name: 'invalid.zip', mimeType: 'application/zip', buffer: Buffer.from('not a zip archive') });
  await ownerPage.getByRole('button', { name: 'Загрузить и проверить тему', exact: true }).click();
  await ownerPage.locator('.theme-issues').waitFor({ timeout: 20000 });
  assert.ok((await ownerPage.locator('.theme-issues').innerText()).trim().length > 10);
  assert.equal(await ownerPage.getByRole('button', { name: 'Сохранить тему', exact: true }).count(), 0);
  const downloadEvent = ownerPage.waitForEvent('download');
  await ownerPage.getByRole('link', { name: 'Скачать пример ZIP ↓', exact: true }).click();
  const download = await downloadEvent, examplePath = join(temporary, 'example.zip');
  await download.saveAs(examplePath); assert.equal(await download.failure(), null);
  const example = await readFile(examplePath);
  const guide = await ownerContext.request.get(base + '/themes/guide.html'); assert.equal(guide.status(), 200);
  assert.match(await guide.text(), /theme\.json/);
  await uploadUI(ownerPage, example, 'example.zip');
  const first = await saveUI(ownerPage);
  assert.ok(first.prepared.assets.length > 0);
  const standardBytes = first.prepared.assets.reduce((n, asset) => n + asset.prepared.standard.size, 0);
  const economyBytes = first.prepared.assets.reduce((n, asset) => n + asset.prepared.economy.size, 0);
  assert.ok(standardBytes <= 2 * 1024 * 1024 && economyBytes <= 1024 * 1024);
  report.upload = { invalid_diagnostics: true, ui_sample_saved: true, archive_bytes: example.length, standard_bytes: standardBytes, economy_bytes: economyBytes };
  at('desktop and mobile preview, both qualities');
  await ownerPage.locator('.theme-preview').waitFor();
  await decorLoaded(ownerPage, '.theme-preview img');
  await ownerPage.screenshot({ path: output + '/themes-preview-desktop.png', fullPage: true });
  await ownerPage.getByLabel('Размер предпросмотра темы', { exact: true }).selectOption('mobile');
  await ownerPage.getByLabel('Качество предпросмотра темы', { exact: true }).selectOption('economy');
  await ownerPage.locator('.theme-preview.mobile').waitFor(); await decorLoaded(ownerPage, '.theme-preview img');
  assert.equal(await ownerPage.locator('.theme-preview-players .panel').count(), 12);
  await ownerPage.screenshot({ path: output + '/themes-preview-mobile.png', fullPage: true });
  const original = await ownerContext.request.get(`${base}/api/themes/${first.card.id}/archive?revision=1`);
  assert.equal(original.status(), 200); assert.deepEqual(await original.body(), example, 'original archive bytes changed');

  at('create custom room through chooser');
  const document = { id: randomUUID(), version: '5.0', name: 'Оформление и рабочая кнопка', language: 'ru', info: {}, rounds: [{ id: randomUUID(), name: 'Раунд', type: 'standard', info: {}, themes: [{ id: randomUUID(), name: 'Оформление', info: {}, questions: [100, 200].map(price => ({ id: randomUUID(), type: 'simple', price, info: {}, params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: `Вопрос за ${price}` }] }], right: ['Верный ответ'] })) }] }] };
  const draft = await request(ownerContext, 'POST', '/drafts', { document });
  await ownerPage.goto(`${base}/studio/${draft.card.id}`);
  await ownerPage.waitForLoadState('networkidle');
  await ownerPage.getByText('Сыграть в этот пакет', { exact: true }).click();
  await ownerPage.getByLabel('Название комнаты', { exact: true }).fill('Проверка оформления');
  await ownerPage.getByLabel('Моя тема', { exact: true }).selectOption(first.card.id);
  const rules = ownerPage.getByText('Правила и таймеры', { exact: true }).locator('..');
  await rules.locator('summary').click();
  await rules.getByLabel(/^Пресет/).selectOption('friends');
  for (const label of ['Выбор вопроса, секунд', 'Ответ после кнопки, секунд', 'Спецвопрос, секунд', 'Ставка, секунд', 'Финал, секунд']) await rules.getByLabel(label, { exact: true }).fill('120');
  const created = ownerPage.waitForResponse(response => new URL(response.url()).pathname === '/api/rooms' && response.request().method() === 'POST');
  await ownerPage.getByRole('button', { name: 'Создать комнату →', exact: true }).click();
  const createdResponse = await created; assert.equal(createdResponse.status(), 201, await createdResponse.text());
  const credentials = await createdResponse.json(); host.credentials = credentials;
  await ownerPage.waitForURL(/\/rooms\/[a-f0-9]{32}/);
  await until(() => host.state?.gameplay?.version === 2, 'host snapshot absent');
  await shell(host, 'custom'); await decorLoaded(ownerPage);
  assert.equal(host.state.appearance.hash, first.prepared.hash);
  const room = credentials.room_id;
  const players = [], native = [];
  for (let i = 0; i < 12; i++) {
    const player = await request(ownerContext, 'POST', `/rooms/${room}/participants`, { name: `Игрок ${i + 1}`, role: 'player', invite_token: credentials.invite_token });
    if (i < 3) players.push(await browserPlayer(player, `player-${i + 1}`, i === 0 ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, reducedMotion: 'reduce' } : {}));
    else native.push(await nativePlayer(player));
  }
  const [mobile, slow, broken] = players;
  await until(() => host.state.participants.filter(p => p.role === 'player').length === 12 && sockets.every(socket => socket.readyState === WebSocket.OPEN), 'room does not have 12 connected players');
  for (const p of players) { await shell(p, 'custom'); await decorLoaded(p.page); }
  assert.equal(await mobile.page.locator('.score-item').count(), 12);
  assert.equal(await mobile.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile room overflows');
  assert.ok(mobile.assetRequests.every(r => r.quality === 'economy'), 'mobile did not default to economy');
  const participantArchive = await mobile.page.request.get(`${base}/api/themes/${first.card.id}/archive?revision=1`, { headers: { Authorization: `Bearer ${mobile.credentials.token}` } });
  assert.ok([401, 403].includes(participantArchive.status()), 'participant downloaded owner-only original');
  const publicTheme = await request(mobile.page.context(), 'GET', `/rooms/${room}/appearance`, undefined, mobile.credentials.token);
  assert.equal(publicTheme.status, 'ready');
  const publicJSON = JSON.stringify(publicTheme);
  assert.equal(publicJSON.includes(first.prepared.original_key), false, 'room manifest leaked source object key');
  for (const asset of first.prepared.assets) assert.equal(publicJSON.includes(asset.prepared.standard.key), false, 'room manifest leaked private variant key');

  at('independent tabs, contextual catalogue and local preferences');
  const independent = observe(await ownerContext.newPage(), 'independent');
  await independent.page.goto(base + '/');
  assert.notEqual(await independent.page.locator('.app-shell').getAttribute('data-theme'), 'custom', 'direct catalogue inherited room appearance');
  await independent.page.goto(`${base}/?room=${room}`);
  await shell(independent, 'classic');
  assert.equal(independent.assetRequests.length, 0, 'new tab inherited room credentials');
  const catalogue = observe(await mobile.page.context().newPage(), 'catalogue', mobile.credentials);
  await catalogue.page.addInitScript(value => sessionStorage.setItem('room:' + value.room_id, JSON.stringify(value)), mobile.credentials);
  await catalogue.page.goto(`${base}/?room=${room}`);
  await shell(catalogue, 'custom'); await decorLoaded(catalogue.page);
  assert.equal(catalogue.wire.length, 0, 'readonly catalogue received game snapshots');
  assert.equal(host.state.phase, 'lobby', 'readonly appearance channel changed room phase');
  await catalogue.page.getByLabel('Поиск пакетов', { exact: true }).fill('ничего');
  await catalogue.page.getByRole('button', { name: 'Найти', exact: true }).click();
  await until(() => new URL(catalogue.page.url()).searchParams.get('q') === 'ничего', 'catalogue filter did not navigate');
  assert.equal(new URL(catalogue.page.url()).searchParams.get('room'), room, 'catalogue filter lost room context');
  await shell(catalogue, 'custom');
  await preference(mobile, 'Классическое оформление', true); await shell(mobile, 'classic'); await shell(host, 'custom'); await shell(catalogue, 'custom');
  await preference(mobile, 'Экономное качество оформления', false);
  const beforeStandard = mobile.assetRequests.length;
  await preference(mobile, 'Классическое оформление', false); await shell(mobile, 'custom'); await decorLoaded(mobile.page);
  assert.ok(mobile.assetRequests.slice(beforeStandard).every(r => r.quality === 'standard'));
  const savedPreferences = await mobile.page.evaluate(id => JSON.parse(sessionStorage.getItem('appearance:' + id)), room);
  assert.deepEqual(savedPreferences, { classic: false, economy: false });
  await mobile.page.reload(); await shell(mobile, 'custom'); await decorLoaded(mobile.page);
  assert.equal(await mobile.page.getByLabel('Экономное качество оформления', { exact: true }).isChecked(), false);
  await mobile.page.screenshot({ path: output + '/themes-player-mobile.png', fullPage: true });
  await catalogue.page.screenshot({ path: output + '/themes-catalogue-context.png', fullPage: true });

  at('slow and broken decoration cannot block twelve-player buzzer');
  const hold = new Promise(resolve => { releaseSlow = resolve; });
  let heldSeen = false, brokenSeen = false;
  await slow.page.route('**/api/rooms/*/appearance/assets/*', async route => {
    if (!heldSeen) { heldSeen = true; slowHeld = true; await hold; slowHeld = false; }
    await route.continue().catch(() => {});
  });
  await broken.page.route('**/api/rooms/*/appearance/assets/*', async route => { brokenSeen = true; await route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }); });
  await preference(slow, 'Экономное качество оформления', true);
  await preference(broken, 'Экономное качество оформления', true);
  await until(() => heldSeen && brokenSeen, 'decoration delay/failure not exercised');
  const hostAssets = host.assetRequests.length, mobileAssets = mobile.assetRequests.length;
  await click(host, 'Начать раунд'); await stage(host, 'select_question');
  await click(host, 'Оформление, 100 очков'); await stage(host, 'present_question');
  await click(host, 'Открыть кнопку'); await stage(host, 'buzz_open');
  await stage(mobile, 'buzz_open');
  assert.equal(slowHeld, true, 'slow fixture released before game check');
  await mobile.page.getByRole('button', { name: 'Знаю ответ!', exact: true }).click();
  await stage(host, 'collect_answer');
  assert.equal(host.state.gameplay.stage.turn_id, mobile.credentials.participant_id);
  await host.page.getByRole('button', { name: /^✓ Верно/ }).click(); await stage(host, 'resolved');
  assert.equal(host.assetRequests.length, hostAssets, 'unrelated game snapshots refetched host decoration');
  assert.equal(mobile.assetRequests.length, mobileAssets, 'unrelated game snapshots refetched player decoration');
  await shell(broken, 'classic'); await broken.page.locator('.theme-fallback').waitFor();
  releaseSlow();
  await slow.page.unroute('**/api/rooms/*/appearance/assets/*');
  await broken.page.unroute('**/api/rooms/*/appearance/assets/*');
  await preference(broken, 'Экономное качество оформления', false); await shell(broken, 'custom');
  report.game = { players: 12, chromium_players: 3, websocket_players: 9, slow_decor_buzzer: true, broken_decor_fallback: true, no_snapshot_refetch: true, mobile_overflow: false };

  at('pause, builtin/custom switch, keyboard focus and reload');
  await click(host, 'Пауза Ⅱ'); await until(() => host.state.phase === 'paused', 'game did not pause');
  await applyAppearance(host, { builtin: 'pirate' });
  await shell(host, 'pirate'); await shell(mobile, 'pirate'); await shell(catalogue, 'pirate');
  await until(() => mobile.page.locator('.builtin-art').evaluateAll(images => images.length === 1 && images[0].complete && images[0].naturalWidth > 0), 'builtin illustration did not load');
  assert.equal(host.state.phase, 'paused');
  await applyAppearance(host, { theme_id: first.card.id });
  await shell(host, 'custom'); await shell(mobile, 'custom'); await shell(catalogue, 'custom');
  assert.equal(host.state.phase, 'paused');
  await mobile.page.reload(); await shell(mobile, 'custom'); await decorLoaded(mobile.page);
  await until(() => mobile.state?.phase === 'paused', 'reconnect lost pause');
  await mobile.page.keyboard.press('Tab');
  const focus = await mobile.page.evaluate(() => ({ tag: document.activeElement?.tagName, visible: document.activeElement?.matches(':focus-visible') }));
  assert.ok(focus.visible && ['A', 'BUTTON', 'INPUT', 'SUMMARY', 'SELECT'].includes(focus.tag), 'keyboard focus not visible');
  assert.equal(await mobile.page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches), true);
  const decorative = await mobile.page.locator('.theme-background, .theme-ornament').evaluateAll(images => images.every(image => getComputedStyle(image).pointerEvents === 'none'));
  assert.equal(decorative, true, 'decor intercepts pointer input');

  at('new library version and ordinary deletion preserve room pin');
  const libraryPage = await ownerContext.newPage(); observe(libraryPage, 'library');
  await libraryPage.goto(base + '/themes');
  await libraryPage.locator('.theme-library-list button').filter({ hasText: first.card.name }).waitFor();
  const revised = await changedArchive(example, 'Обновлённая тема ' + suffix);
  await uploadUI(libraryPage, revised, 'revision.zip');
  const second = await saveUI(libraryPage, first.card.id);
  assert.equal(second.card.revision, 2); assert.notEqual(second.prepared.hash, first.prepared.hash);
  let pinned = await request(ownerContext, 'GET', `/rooms/${room}/appearance`, undefined, credentials.token);
  assert.equal(pinned.appearance.revision, 1); assert.equal(pinned.appearance.hash, first.prepared.hash);
  await libraryPage.getByRole('button', { name: 'Удалить тему', exact: true }).click();
  await libraryPage.getByRole('button', { name: 'Подтвердить удаление темы', exact: true }).click();
  await until(async () => (await request(ownerContext, 'GET', '/themes')).themes.length === 0, 'deleted theme still listed');
  pinned = await request(ownerContext, 'GET', `/rooms/${room}/appearance`, undefined, credentials.token);
  assert.equal(pinned.status, 'ready'); assert.equal(pinned.appearance.hash, first.prepared.hash);
  const retainedAsset = await mobile.page.request.get(`${base}/api/rooms/${room}/appearance/assets/0?hash=${first.prepared.hash}&quality=economy`, { headers: { Authorization: `Bearer ${mobile.credentials.token}` } });
  assert.equal(retainedAsset.status(), 200, 'ordinary deletion broke pinned derivative');
  await mobile.page.reload(); await shell(mobile, 'custom'); await decorLoaded(mobile.page);
  assert.equal(host.state.appearance.revision, 1);

  // Load the second question before revocation. The account-deletion fallback
  // also revokes the author's pack; already loaded game content remains usable.
  await click(host, 'Продолжить ▶'); await until(() => host.state.phase !== 'paused', 'game did not resume');
  await click(host, 'Оформление, 200 очков'); await stage(host, 'present_question');
  await click(host, 'Открыть кнопку'); await stage(mobile, 'buzz_open');
  await click(host, 'Пауза Ⅱ'); await until(() => host.state.phase === 'paused', 'second question did not pause');

  at('moderation revokes room and readonly catalogue through WebSocket');
  const roomRefreshes = mobile.refreshes, catalogueRefreshes = catalogue.refreshes;
  if (process.env.DATABASE_URL) {
    moderatorContext = await context();
    const username = 'theme_mod_' + suffix;
    await request(moderatorContext, 'POST', '/account/register', { username, password, display_name: 'Модератор оформления' }); moderatorRegistered = true;
    await run(process.env.GO || 'go', ['run', './cmd/admin', 'role', username, 'moderator'], { cwd: root, env: process.env, timeout: 60000, maxBuffer: 1024 * 1024 });
    await request(moderatorContext, 'POST', `/themes/${first.card.id}/block`, { blocked: true, reason: 'Проверка отзыва оформления в изолированном браузерном сценарии' });
    revocation = 'moderator_block';
  } else {
    await request(ownerContext, 'DELETE', '/account/me', { password }); registered = false;
    revocation = 'account_deletion';
  }
  await until(() => mobile.refreshes > roomRefreshes && catalogue.refreshes > catalogueRefreshes, 'revocation notification did not reach both room and readonly catalogue', 15000);
  await shell(mobile, 'classic'); await shell(catalogue, 'classic'); await shell(host, 'classic');
  await mobile.page.locator('.theme-fallback').waitFor(); await catalogue.page.locator('.theme-fallback').waitFor();
  assert.equal(catalogue.wire.length, 0, 'readonly channel received private gameplay');
  const revoked = await request(mobile.page.context(), 'GET', `/rooms/${room}/appearance`, undefined, mobile.credentials.token);
  assert.equal(revoked.status, 'unavailable'); assert.equal(revoked.manifest, undefined);
  const blockedAsset = await mobile.page.request.get(`${base}/api/rooms/${room}/appearance/assets/0?hash=${first.prepared.hash}&quality=economy`, { headers: { Authorization: `Bearer ${mobile.credentials.token}` } });
  assert.ok([403, 404, 409].includes(blockedAsset.status()), `revoked derivative status ${blockedAsset.status()}`);
  await click(host, 'Продолжить ▶'); await until(() => host.state.phase !== 'paused', 'theme revocation blocked game resume');
  await stage(mobile, 'buzz_open');
  await mobile.page.getByRole('button', { name: 'Знаю ответ!', exact: true }).click(); await stage(host, 'collect_answer');
  await host.page.screenshot({ path: output + '/themes-revoked-game-active.png', fullPage: true });
  await catalogue.page.screenshot({ path: output + '/themes-revoked-catalogue.png', fullPage: true });
  assert.equal(await mobile.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  assert.deepEqual(native.flatMap(client => client.errors), [], 'native players received errors');
  assert.deepEqual(pageErrors, [], 'uncaught browser errors');
  assert.deepEqual(externalRequests, [], 'unexpected external resource request');
  Object.assign(report, { desktop_mobile_preview: true, both_qualities: true, owner_original_only: true, original_bytes_preserved: true, readonly_catalogue_context: true, independent_tab: true, local_classic_and_economy: true, preference_reload: true, paused_switch: true, builtin_art: true, keyboard_focus: true, reduced_motion: true, new_version_pin: true, ordinary_delete_pin: true, revocation, websocket_fallback: true, buzzer_after_revocation: true, external_requests: 0, page_errors: 0 });
  await writeFile(output + '/themes-report.json', JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
} catch (error) {
  await writeFile(output + '/themes-failure.json', JSON.stringify({ checkpoint, message: error.message, pageErrors, externalRequests, clients: clients.map(c => { const url = new URL(c.page.url()); return { name: c.name, url: url.origin + url.pathname + url.search, phase: c.state?.phase, stage: c.state?.gameplay?.stage?.kind, appearance: c.state?.appearance, refreshes: c.refreshes, assets: c.assetRequests }; }) }, null, 2));
  for (const client of clients) await client.page.screenshot({ path: `${output}/themes-failure-${client.name}.png`, fullPage: true }).catch(() => {});
  throw new Error(`${checkpoint}: ${error.message}`, { cause: error });
} finally {
  releaseSlow();
  if (registered) await request(ownerContext, 'DELETE', '/account/me', { password }).catch(() => {});
  if (moderatorRegistered) await request(moderatorContext, 'DELETE', '/account/me', { password }).catch(() => {});
  for (const socket of sockets) socket.close();
  for (const c of contexts) await c.close().catch(() => {});
  await browser.close();
  await rm(temporary, { recursive: true });
}
