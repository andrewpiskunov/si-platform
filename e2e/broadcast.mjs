import { chromium } from 'playwright';
import assert from 'node:assert/strict';
const base = process.env.BASE_URL || 'http://localhost:5173';
const browser = await chromium.launch();
const page = await browser.newPage();
const errors = [];
page.on('pageerror', e => errors.push(e.message));
try {
  await page.goto(base);
  await page.locator('.app-shell[data-theme="classic"]').waitFor();
  await page.evaluate(() => document.fonts.ready);
  assert.match(await page.locator('.app-shell').evaluate(el => getComputedStyle(el).fontFamily), /Golos/);
  // A custom preview inside the default shell must not inherit the blue board paint.
  await page.evaluate(() => {
    const preview = document.createElement('div');
    preview.className = 'theme-preview'; preview.dataset.theme = 'custom';
    preview.style.cssText = '--surface:#ffffff;--surface-2:#eeeeee;--text:#111111;--accent:#222222;--accent-foreground:#111111;--theme-font:Georgia,serif;--button-radius:14px;--button-border:2px;--card-radius:14px;--card-border:2px';
    preview.innerHTML = '<div class="board"><div class="board-row"><div class="board-theme">Тема</div><button class="board-cell">100</button></div></div>';
    document.querySelector('main').append(preview);
  });
  assert.equal(await page.locator('.theme-preview .board-cell').evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(238, 238, 238)');
  assert.match(await page.locator('.theme-preview .board-cell').evaluate(el => getComputedStyle(el).fontFamily), /Georgia/);
  assert.equal(await page.locator('.theme-preview .board-cell').evaluate(el => getComputedStyle(el).borderRadius), '14px');
  await page.locator('.theme-preview').evaluate(el => el.remove());
  for (const width of [360, 390, 768, 1440]) {
    await page.setViewportSize({width, height:900});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `catalogue overflow at ${width}`);
  }
  assert.deepEqual(errors, []);
  console.log('Broadcast default, nested custom palette/font/shape, responsive catalogue PASS');
} finally { await browser.close(); }
