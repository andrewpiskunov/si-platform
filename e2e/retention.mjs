import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const base = process.env.BASE_URL || 'http://localhost:5173';
assert.ok(process.env.DATABASE_URL, 'Local preview DATABASE_URL required');
const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage(), errors = [];
page.on('pageerror', e => errors.push(e.message));
let registered = false, host, checkpoint = 'registration';
const username = 'retention_' + randomBytes(6).toString('hex');
const password = randomBytes(20).toString('hex');
async function api(method, path, data) {
  const response = await context.request.fetch(base + '/api' + path, { method, data });
  assert.ok(response.ok(), `${path}: ${response.status()}`);
  return response.status() === 204 ? null : response.json();
}
try {
  await api('POST', '/account/register', { username, password, display_name: 'Срок комнаты' });
  registered = true;
  await page.addInitScript(() => {
    const Native = WebSocket;
    window.__sockets = [];
    window.WebSocket = class extends Native {
      constructor(...args) { super(...args); window.__sockets.push(this); }
    };
  });
  checkpoint = 'room creation';
  await page.goto(base + '/studio');
  const details = page.locator('details').filter({ has: page.locator('summary').filter({ hasText: /^Быстро попробовать игру$/ }) });
  await details.locator('summary').first().click();
  const response = page.waitForResponse(r => r.url() === base + '/api/rooms' && r.request().method() === 'POST');
  await details.getByRole('button', { name: 'Создать комнату →', exact: true }).click();
  host = await (await response).json();
  assert.match(host.room_id, /^[a-f0-9]{32}$/);
  await page.getByRole('status').filter({ hasText: 'В сети' }).waitFor();
  const notice = page.getByLabel('Срок хранения комнаты', { exact: true });
  await notice.locator('time').waitFor();
  assert.ok(Date.parse(await notice.locator('time').getAttribute('datetime')) > Date.now());
  const policy = await context.newPage();
  await policy.goto(base + '/privacy');
  await policy.getByRole('heading', { name: /Хранение/ }).first().waitFor();
  await policy.close();
  checkpoint = 'expire only own synthetic room';
  // Strictly validated generated ID; no user input or broad database fixture aging.
  const { stdout } = await promisify(execFile)('psql', [process.env.DATABASE_URL, '-X', '-v', 'ON_ERROR_STOP=1', '-Atc',
    `UPDATE rooms SET last_activity_at=clock_timestamp()-interval '8 days' WHERE id='${host.room_id}' RETURNING id`]);
  assert.ok(stdout.includes(host.room_id));
  // An existing actor must reject activity immediately, even before its minute sweep.
  await page.evaluate(({ id, room }) => {
    const socket = window.__sockets.find(s => s.readyState === WebSocket.OPEN && new URL(s.url).pathname === `/api/rooms/${room}/ws` && !s.url.includes('appearance_only'));
    if (!socket) throw Error('Missing live socket');
    socket.send(JSON.stringify({ v: 1, id, type: 'finish' }));
  }, { id: randomUUID(), room: host.room_id });
  await page.getByRole('alert', { name: 'Комната недоступна', exact: true }).waitFor({ timeout: 15000 });
  assert.equal(await page.evaluate(id => sessionStorage.getItem('room:' + id), host.room_id), null);
  const connections = await page.evaluate(() => window.__sockets.length);
  await page.waitForTimeout(2500);
  assert.equal(await page.evaluate(() => window.__sockets.length), connections, 'expired room reconnects');
  assert.equal(await notice.count(), 0);
  await page.getByRole('link', { name: 'Выйти к списку комнат' }).click();
  await page.waitForURL(base + '/rooms');
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ retention: 'visible date + public policy', expiry: 'terminal before sweep, credentials cleared, no reconnect', pageErrors: errors }));
} catch (error) { console.error('checkpoint: ' + checkpoint); throw error; }
finally {
  if (registered) await context.request.delete(base + '/api/account/me', { data: { password } }).catch(() => {});
  await context.close(); await browser.close();
}
