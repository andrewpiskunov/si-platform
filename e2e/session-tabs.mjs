import { chromium } from 'playwright';
import assert from 'node:assert/strict';
const base = process.env.BASE_URL || 'http://localhost:5173';
const browser = await chromium.launch();
const context = await browser.newContext();
if (process.env.AUTH_STORAGE_FALLBACK === '1') await context.addInitScript(() => { Object.defineProperty(window, 'BroadcastChannel', {value: undefined}); });
const errors = [];
let identity = { id: 'one', username: 'one', display_name: 'One', role: 'author' };
let release;
let held;
const waiting = new Promise(resolve => held = resolve);
let delayed = false;
function usage(n) {
  return { storage: { used_bytes: n, limit_bytes: 10737418240, remaining_bytes: 10737418240-n }, packs: {used:n,limit:200,remaining:200-n},themes:{used:0,limit:100,remaining:100},uploads:{active:0,limit:2,remaining:2},limits:{pack_revisions:500,theme_revisions:100,siq_bytes:262144000,media_bytes:104857600,theme_bytes:5242880} };
}
try {
  await context.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/account/login') { identity = { ...identity, id:'two',username:'two',display_name:'Two' }; return route.fulfill({json:{user:identity}}); }
    if (path === '/api/account/logout') { identity = null; return route.fulfill({json:{ok:true}}); }
    if (path === '/api/account/me') return identity ? route.fulfill({json:{user:identity}}) : route.fulfill({status:401,json:{error:'unauthorized'}});
    if (path === '/api/usage') {
      if (!identity) return route.fulfill({status:401,json:{error:'unauthorized'}});
      const payload = usage(identity.id === 'one' ? 42 : 0);
      if (delayed && !release) { await new Promise(resolve => { release = resolve; held(); }); }
      return route.fulfill({json:payload}).catch(() => {});
    }
    return route.fulfill({json:{tokens:[],packs:[],items:[]}});
  });
  const a = await context.newPage(), b = await context.newPage();
  for (const p of [a,b]) p.on('pageerror', e => errors.push(e.message));
  await a.goto(base+'/account');
  await a.getByTestId('quota-packs').filter({hasText:'42 из 200'}).waitFor();
  delayed = true;
  await a.getByRole('button',{name:'Обновить использование',exact:true}).click();
  await waiting;
  await b.goto(base+'/login');
  await b.locator('.account-link').filter({hasText:'One'}).waitFor();
  await b.getByLabel(/^Логин/).fill('two');
  await b.getByLabel('Пароль', {exact:true}).fill('password');
  await b.getByRole('button',{name:'Войти',exact:true}).click();
  await b.waitForURL('**/studio');
  await b.getByTestId('quota-packs').filter({hasText:'0 из 200'}).waitFor();
  release();
  try { await a.getByTestId('quota-packs').filter({hasText:'0 из 200'}).waitFor({timeout:5000}); } catch(e) { console.error({errors,body:await a.locator('body').innerText()}); throw e; }
  await b.goto(base+'/account');
  await b.getByTestId('quota-packs').filter({hasText:'0 из 200'}).waitFor();
  await b.getByRole('button',{name:'Выйти',exact:true}).click();
  await b.waitForURL('**/login');
  await a.locator('.account-link').filter({hasText:'Войти'}).waitFor();
  await a.getByTestId('quota-usage').waitFor({state:'detached'});
  assert.deepEqual(errors,[]);
  console.log('PASS cross-tab account switch rejects delayed usage; logout clears other tab');
} finally { release?.(); await browser.close(); }
