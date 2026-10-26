import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
const base = process.env.BASE_URL || 'http://localhost:5174';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
const context = await browser.newContext();
const password = randomBytes(20).toString('hex');
let registered = false;
try {
  await mkdir('.local/browser', {recursive:true});
  const response = await context.request.post(base + '/api/account/register', { data: { username: 'oral_' + randomBytes(6).toString('hex'), password, display_name: 'Ведущий' } });
  assert.equal(response.status(), 201); registered = true;
  const rules = { preset:'classic', answer_mode:'oral', auto_open:true, reading_seconds:1, selection_seconds:30, answer_seconds:30, special_seconds:30, wager_seconds:30, final_seconds:60, penalties:true, buzzer_mode:'first', window_ms:200, false_start:true, false_start_seconds:3, no_risk_multiplier:2 };
  const created = await context.request.post(base + '/api/rooms', { data: { host_name:'Ведущий', rules } });
  assert.equal(created.status(), 201); const host = await created.json();
  const hostPage = await context.newPage();
  hostPage.setDefaultTimeout(12000);
  await hostPage.addInitScript(c => sessionStorage.setItem('room:' + c.room_id, JSON.stringify(c)), host);
  await hostPage.goto(base + '/rooms/' + host.room_id);
  const playerPages = [];
  for (let i = 0; i < 2; i++) {
    const ctx = await browser.newContext();
    const joined = await ctx.request.post(`${base}/api/rooms/${host.room_id}/participants`, { data: { name:'Игрок ' + i, role:'player', invite_token:host.invite_token } });
    assert.equal(joined.status(), 201, await joined.text());
    const credentials = await joined.json(); const p = await ctx.newPage();
    await p.addInitScript(c => sessionStorage.setItem('room:' + c.room_id, JSON.stringify(c)), credentials);
    await p.goto(base + '/rooms/' + host.room_id); playerPages.push(p);
  }
  const popup = hostPage.waitForEvent('popup');
  await hostPage.getByRole('button', { name: 'Второй экран ↗', exact: true }).click();
  const screen = await popup;
  await screen.getByRole('heading', { name: 'Собираемся играть', exact: true }).waitFor();
  assert.equal(await screen.locator('.host-console').count(), 0);
  await hostPage.getByRole('button', { name:'Начать раунд', exact:true }).click();
  await hostPage.getByRole('button', { name:'Числа, 100 очков', exact:true }).click();
  await hostPage.locator('.host-answer').getByText('56', { exact:true }).waitFor();
  await screen.getByText('Сколько будет 7 × 8?', { exact:true }).waitFor();
  assert.equal(await screen.getByText('56', { exact:true }).count(), 0);
  await playerPages[0].getByRole('button', { name:'Знаю ответ!', exact:true }).click();
  const judge = hostPage.locator('.host-console').getByRole('button', { name:'✓ Верно', exact:true });
  await judge.waitFor();
  assert.equal(await hostPage.getByRole('button', { name:'Ответ прозвучал', exact:true }).count(), 0);
  assert.equal(await playerPages[0].getByLabel('Текст ответа', { exact:true }).count(), 0);
  for (const viewport of [{width:1366,height:768},{width:1920,height:1080}]) {
    await hostPage.setViewportSize(viewport);
    await hostPage.evaluate(() => window.scrollTo(0,0));
    const bounds = await hostPage.evaluate(() => {
      const box = selector => { const r=document.querySelector(selector).getBoundingClientRect(); return {top:r.top,bottom:r.bottom,width:r.width,height:r.height}; };
      return {height:innerHeight,doc:document.documentElement.scrollHeight,scene:box('.question-stage'),scores:box('.participants-strip'),judge:box('.host-console .primary')};
    });
    assert.ok(bounds.doc <= bounds.height+2, JSON.stringify(bounds));
    assert.ok(bounds.scores.bottom <= bounds.height+1, 'scores below viewport');
    assert.ok(bounds.judge.bottom <= bounds.height, 'judgement below viewport');
    assert.ok(bounds.scene.height >= viewport.height*.45, JSON.stringify(bounds));
    await hostPage.screenshot({path:`.local/browser/game-${viewport.width}.png`});
  }
  await playerPages[0].setViewportSize({width:390,height:844});
  assert.equal(await playerPages[0].evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await judge.click();
  await screen.getByText('56', { exact:true }).waitFor();
  await screen.screenshot({ path: '.local/browser/second-screen.png' });
  console.log('PASS: host viewport at 1366x768 and 1920x1080, mobile width 390, oral judgement and display privacy');
} finally {
  if (registered) await context.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
