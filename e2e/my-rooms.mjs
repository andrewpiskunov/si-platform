import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
const base = process.env.BASE_URL || 'http://localhost:5174';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
const first = await browser.newContext();
const returning = await browser.newContext();
const password = randomBytes(20).toString('hex');
const username = 'return_' + randomBytes(6).toString('hex');
let registered = false;
try {
  const registeredResponse = await first.request.post(base + '/api/account/register', { data: { username, password, display_name: 'Проверка ведущего' } });
  assert.equal(registeredResponse.status(), 201); registered = true;
  const createdResponse = await first.request.post(base + '/api/rooms', { data: { host_name: 'Ведущий', title: 'Моя приватная игра' } });
  assert.equal(createdResponse.status(), 201);
  const original = await createdResponse.json();
  await first.close();
  const login = await returning.request.post(base + '/api/account/login', { data: { username, password } });
  assert.equal(login.status(), 200);
  const page = await returning.newPage();
  page.setDefaultTimeout(10000);
  await page.goto(base + '/account');
  const section = page.locator('#my-rooms');
  await section.getByRole('heading', { name: 'Моя приватная игра' }).waitFor();
  await section.getByText('Вы ведущий', { exact: true }).waitFor();
  await section.getByText('По приглашению', { exact: true }).waitFor();
  assert.equal(await page.evaluate(id => sessionStorage.getItem('room:' + id), original.room_id), null);
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  }
  await mkdir('.local/browser', { recursive: true });
  await page.screenshot({ path: '.local/browser/my-rooms.png' });
  const recoveredResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/session'));
  await section.getByRole('button', { name: 'Продолжить ведение' }).click();
  const response = await recoveredResponse;
  assert.equal(response.status(), 200);
  const recovered = await response.json();
  assert.equal(recovered.participant_id, original.participant_id);
  assert.notEqual(recovered.token, original.token);
  await page.waitForURL('**/rooms/' + original.room_id);
  assert.equal((await page.evaluate(id => JSON.parse(sessionStorage.getItem('room:' + id)), original.room_id)).token, recovered.token);
  const roster = await returning.request.get(base + '/api/rooms/' + original.room_id + '/roster', { headers: { Authorization: 'Bearer ' + recovered.token } });
  assert.equal(roster.status(), 200);
  await page.getByRole('button', { name: 'Создать приглашение', exact: true }).click();
  await page.getByRole('button', { name: 'Пригласить друзей ↗', exact: true }).waitFor();
  const anotherTab = await returning.newPage();
  await anotherTab.goto(base + '/account');
  await anotherTab.locator('#my-rooms').getByRole('button', { name: 'Продолжить ведение' }).click();
  await anotherTab.waitForURL('**/rooms/' + original.room_id);
  await page.getByRole('heading', { name: 'Доступ к комнате прекращён' }).waitFor();
  console.log('PASS: private room in account, fresh browser login, original host seat restored, host controls and responsive account');
} finally {
  if (registered) await returning.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
