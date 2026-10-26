import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
const base = process.env.BASE_URL || 'http://localhost:5174';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
const context = await browser.newContext();
const password = randomBytes(20).toString('hex');
let registered = false;
try {
  await mkdir('.local/browser',{recursive:true});
  const response = await context.request.post(base + '/api/account/register', { data: { username: 'oral_' + randomBytes(6).toString('hex'), password, display_name: 'Ведущий' } });
  assert.equal(response.status(), 201); registered = true;
  const rules = { preset:'classic', answer_mode:'oral', auto_open:true, reading_seconds:1, selection_seconds:30, answer_seconds:30, special_seconds:30, wager_seconds:30, final_seconds:60, penalties:true, buzzer_mode:'first', window_ms:200, false_start:true, false_start_seconds:3, no_risk_multiplier:2 };
  const created = await context.request.post(base + '/api/rooms', { data: { host_name:'Ведущий', rules } });
  assert.equal(created.status(), 201); const host = await created.json();
  const hostPage = await context.newPage();
  hostPage.setDefaultTimeout(12000);
  await hostPage.addInitScript(()=>{
    window.__soundStarts=0;
    const Original=window.AudioContext;
    window.AudioContext=class extends Original {
      createOscillator(){const node=super.createOscillator();const start=node.start.bind(node);node.start=(...args)=>{window.__soundStarts++;return start(...args);};return node;}
    };
  });
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
  assert.equal(await hostPage.evaluate(()=>window.__soundStarts),0);
  await hostPage.locator('.sound-settings summary').click();
  await hostPage.getByRole('button',{name:'Включить звуки',exact:true}).click();
  await hostPage.getByText('Звуки включены в этом окне.',{exact:true}).waitFor();
  assert.ok(await hostPage.evaluate(()=>window.__soundStarts)>0);
  await hostPage.getByRole('slider',{name:'Громкость',exact:true}).fill('25');
  await hostPage.locator('.sound-settings summary').click();
  const afterEnable=await hostPage.evaluate(()=>window.__soundStarts);
  await hostPage.getByRole('button', { name:'Начать раунд', exact:true }).click();
  await hostPage.getByRole('button', { name:'Числа, 100 очков', exact:true }).click();
  await hostPage.locator('.host-answer').getByText('56', { exact:true }).waitFor();
  await screen.getByText('Сколько будет 7 × 8?', { exact:true }).waitFor();
  assert.equal(await screen.getByText('56', { exact:true }).count(), 0);
  await playerPages[0].getByRole('button', { name:'Знаю ответ!', exact:true }).click();
  const judge = hostPage.locator('.host-console').getByRole('button', { name:'✓ Верно', exact:true });
  await judge.waitFor();
  await hostPage.locator('.answer-feedback').getByText('Отвечает · Игрок 0',{exact:true}).waitFor();
  await screen.locator('.answer-feedback').getByText('Отвечает · Игрок 0',{exact:true}).waitFor();
  assert.equal(await hostPage.getByRole('button', { name:'Ответ прозвучал', exact:true }).count(), 0);
  assert.equal(await playerPages[0].getByLabel('Текст ответа', { exact:true }).count(), 0);
  await hostPage.waitForFunction(n=>window.__soundStarts>n,afterEnable);
  await judge.click();
  await screen.getByText('56', { exact:true }).waitFor();
  await hostPage.locator('.answer-feedback').getByText('Верно · Игрок 0',{exact:true}).waitFor();
  await hostPage.locator('.score-item').filter({hasText:'Игрок 0'}).getByText('Верно · +100',{exact:true}).waitFor();
  await hostPage.screenshot({path:'.local/browser/answer-feedback.png'});
  await screen.screenshot({ path: '.local/browser/second-screen.png' });
  await hostPage.locator('.sound-settings summary').click();
  await hostPage.getByRole('button',{name:'Выключить звуки',exact:true}).click();
  await hostPage.getByText('Звуки выключены.',{exact:true}).waitFor();
  assert.equal(await screen.locator('.sound-settings summary').innerText(),'Звук: выкл');
  await hostPage.locator('.sound-settings summary').click();
  const mutedCount=await hostPage.evaluate(()=>window.__soundStarts);
  await hostPage.getByRole('button',{name:'Числа, 200 очков',exact:true}).click();
  await hostPage.locator('.host-answer').getByText('Пи',{exact:true}).waitFor();
  assert.equal(await hostPage.evaluate(()=>window.__soundStarts),mutedCount);
  console.log('PASS: audio opt-in, event cues, volume, mute and independent second screen; oral game, immediate host answer/judgement, no player text field, live second screen without private answer');
} finally {
  if (registered) await context.request.delete(base + '/api/account/me', { data: { password } });
  await browser.close();
}
