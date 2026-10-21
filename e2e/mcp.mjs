import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
const base = process.env.BASE_URL || 'http://localhost:5173';
const browser = await chromium.launch(), context = await browser.newContext(), page = await context.newPage();
const errors = []; page.on('pageerror', e => errors.push(e.message));
const password = randomBytes(20).toString('hex');
let registered = false, checkpoint = 'register', token = '', rpcID = 0;
async function api(method, path, data) {
  const end = Date.now() + 60000;
  for (;;) {
    const r = await context.request.fetch(base + '/api' + path, { method, data });
    if (r.status() === 429 && path.startsWith('/account/') && Date.now() < end) {
      const seconds = Number(r.headers()['retry-after']); assert.ok(seconds > 0 && seconds <= 10);
      await new Promise(resolve => setTimeout(resolve, seconds * 1000)); continue;
    }
    assert.ok(r.ok(), `${path}: ${r.status()}`); return r.status() === 204 ? null : r.json();
  }
}
async function rpc(method, params) {
  const r = await context.request.post(base + '/api/mcp', { headers: { Authorization: 'Bearer ' + token, Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-11-25' }, data: { jsonrpc: '2.0', id: ++rpcID, method, params } });
  assert.equal(r.status(), 200); const body = await r.json(); assert.equal(body.error, undefined); return body.result;
}
async function tool(name, args, fail = false) {
  const r = await rpc('tools/call', { name, arguments: args });
  assert.equal(Boolean(r.isError), fail, `${name}: unexpected result`);
  return fail ? r : JSON.parse(r.content[0].text);
}
async function until(fn) {
  const end = Date.now() + 20000;
  while (Date.now() < end) { if (await fn()) return; await new Promise(resolve => setTimeout(resolve, 100)); }
  throw Error('durable editor change timed out');
}
try {
  await api('POST', '/account/register', { username: 'mcp_' + randomBytes(6).toString('hex'), password, display_name: 'MCP author' }); registered = true;
  await page.goto(base + '/account');
  await page.getByTestId('mcp-connection').locator('summary').click();
  assert.ok((await page.getByTestId('mcp-connection').textContent()).includes(base + '/api/mcp'));
  await page.getByLabel('Название', { exact: true }).fill('Browser MCP test');
  await page.getByLabel('Редактирование черновиков', { exact: true }).check();
  await page.getByLabel('Экспорт пакетов', { exact: true }).check();
  await page.getByRole('button', { name: 'Создать токен', exact: true }).click();
  const issued = page.locator('.notice.success .code-token'); await issued.waitFor(); token = (await issued.textContent()).trim();
  assert.equal(token.length, 43);
  checkpoint = 'real MCP authoring';
  await rpc('initialize', { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'browser-test', version: '1' } });
  assert.equal((await rpc('tools/list', {})).tools.length, 5);
  const document = { id: 'doc', name: 'MCP browser pack', version: '5.0', info: {}, rounds: [{ id: 'r1', name: 'Round', info: {}, themes: [{ id: 't1', name: 'Theme', info: {}, questions: [{ id: 'q1', price: 100, info: {}, right: ['Answer'], params: [{ name: 'question', type: 'content', items: [{ type: 'text', value: 'Question' }] }] }] }] }] };
  const create = { idempotency_key: 'browser-create-key-01', document };
  const card = await tool('create_draft', create), duplicate = await tool('create_draft', create);
  assert.equal(card.id, duplicate.id); assert.equal(card.status, 'draft');
  await page.goto(base + '/studio/' + card.id);
  await page.getByLabel('Название', { exact: true }).fill('Changed in browser');
  await until(async () => (await api('GET', '/drafts/' + card.id)).document.name === 'Changed in browser');
  const question = structuredClone(document.rounds[0].themes[0].questions[0]); question.right = ['MCP edited answer'];
  const patch = { idempotency_key: 'browser-upsert-key-01', pack_id: card.id, base_revision: 1, round_id: 'r1', theme_id: 't1', questions: [question] };
  await tool('upsert_questions', patch, true);
  const current = await tool('get_draft', { pack_id: card.id }); patch.base_revision = current.card.revision;
  const updated = await tool('upsert_questions', patch); assert.equal(updated.revision, current.card.revision + 1);
  const downloaded = await tool('export_siq', { pack_id: card.id });
  const file = await context.request.get(base + downloaded.download_path, { headers: { Authorization: 'Bearer ' + token } });
  assert.equal(file.status(), 200); assert.equal((await file.body()).subarray(0, 2).toString(), 'PK');
  assert.equal((await context.request.get(base + '/api/catalogue/' + card.id)).status(), 404);
  checkpoint = 'connection guide mobile and revocation';
  await page.goto(base + '/account'); await page.getByTestId('mcp-connection').locator('summary').click();
  for (const width of [390, 820, 1440]) { await page.setViewportSize({ width, height: 900 }); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false); }
  await page.getByRole('button', { name: 'Отозвать', exact: true }).click();
  await until(async () => (await api('GET', '/account/tokens')).tokens.every(t => t.revoked_at));
  const denied = await context.request.post(base + '/api/mcp', { headers: { Authorization: 'Bearer ' + token }, data: { jsonrpc: '2.0', id: 1, method: 'tools/list' } }); assert.equal(denied.status(), 401);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ mcp: 'pass', guide: true, scoped_token: true, real_tools: 5, idempotent_create: true, editor_conflict: true, private_siq_download: true, revoke: true, mobile: true, page_errors: 0 }));
} catch (e) { console.error({ checkpoint, url: page.url(), errors, message: e.message }); throw e; }
finally { try { if (registered) await api('DELETE', '/account/me', { password }); } finally { await browser.close(); } }
