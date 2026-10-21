import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
const base = process.env.BASE_URL || 'http://localhost:5173';
const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();
const errors = [], sourceRequests = [];
page.on('request', r => { if (r.url().includes('example.invalid')) sourceRequests.push(r.url()); });
page.on('pageerror', e => errors.push(e.message));
const password = randomBytes(20).toString('hex');
let registered = false, checkpoint = 'register';
async function api(method, path, data) {
  const end = Date.now() + 60000;
  for (;;) {
    const r = await context.request.fetch(base + '/api' + path, { method, data });
    if (r.status() === 429 && path.startsWith('/account/') && Date.now() < end) {
      const seconds = Number(r.headers()['retry-after']);
      assert.ok(seconds > 0 && seconds <= 10);
      await new Promise(resolve => setTimeout(resolve, seconds * 1000));
      continue;
    }
    assert.ok(r.ok(), `${path}: ${r.status()}`);
    return r.status() === 204 ? null : r.json();
  }
}
async function until(fn, message) {
  const end = Date.now() + 30000;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw Error(message);
}
try {
  await api('POST', '/account/register', { username: 'ai_' + randomBytes(6).toString('hex'), password, display_name: 'AI browser acceptance' });
  registered = true;
  await page.goto(base + '/studio');
  const panel = page.getByTestId('ai-authoring');
  await panel.locator('summary').click();
  await panel.getByTestId('ai-fixture').waitFor();
  checkpoint = 'quote and ambiguous retry';
  await panel.getByLabel('Тема запроса', { exact: true }).fill('История мореплавания');
  await panel.getByLabel('Вопросов в теме', { exact: true }).fill('2');
  await panel.getByRole('button', { name: 'Рассчитать оценку', exact: true }).click();
  await panel.getByTestId('ai-quote').waitFor();
  const starts = [];
  await page.route('**/api/ai/jobs', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    assert.equal(response.status(), 201);
    starts.push({ request: route.request().postDataJSON(), job: await response.json() });
    if (starts.length === 1) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'unavailable' }) });
    return route.fulfill({ response });
  });
  await panel.getByRole('button', { name: 'Запустить по оценке', exact: true }).click();
  await panel.getByRole('button', { name: 'Повторить запуск с тем же ключом', exact: true }).click();
  await panel.getByLabel('Вопрос 1.1', { exact: true }).waitFor();
  assert.equal(starts.length, 2);
  assert.deepEqual(starts[0].request, starts[1].request);
  assert.equal(starts[0].job.id, starts[1].job.id);
  await page.unroute('**/api/ai/jobs');
  checkpoint = 'review private draft';
  await panel.getByLabel('Вопрос 1.1', { exact: true }).fill('Проверенный автором вопрос');
  await panel.getByLabel('Ответ 1.1', { exact: true }).fill('Ответ автора');
  await panel.getByLabel('Источники 1.1 (до 5, по одному на строку)', { exact: true }).fill('https://example.invalid/unverified');
  await panel.getByRole('checkbox').check();
  const applied = page.waitForResponse(r => r.url().endsWith('/draft') && r.request().method() === 'POST');
  await panel.getByRole('button', { name: 'Создать частный черновик', exact: true }).click();
  const draftResponse = await applied;
  assert.equal(draftResponse.status(), 200);
  const draft = await draftResponse.json();
  assert.equal(draft.card.status, 'draft');
  assert.equal(draft.card.published_revision || 0, 0);
  await page.waitForURL('**/studio/' + draft.card.id);
  const stored = await api('GET', '/drafts/' + draft.card.id);
  assert.ok(JSON.stringify(stored.document).includes('Проверенный автором вопрос'));
  assert.ok(JSON.stringify(stored.document).includes('факты/источники не проверены'));
  assert.equal((await context.request.get(base + '/api/catalogue/' + draft.card.id)).status(), 404);
  checkpoint = 'append theme';
  const editorPanel = page.getByTestId('ai-authoring');
  await editorPanel.locator('summary').click();
  await editorPanel.getByLabel(/^Что подготовить/).selectOption('theme');
  await editorPanel.getByLabel('Тема запроса', { exact: true }).fill('Морские открытия');
  await editorPanel.getByLabel('Вопросов в теме', { exact: true }).fill('1');
  // Durable quote cooldown is part of the real service contract.
  await new Promise(resolve => setTimeout(resolve, 10500));
  await editorPanel.getByRole('button', { name: 'Рассчитать оценку', exact: true }).click();
  await editorPanel.getByRole('button', { name: 'Запустить по оценке', exact: true }).click();
  await editorPanel.getByLabel('Вопрос 1.1', { exact: true }).waitFor();
  await editorPanel.getByLabel(/^Обычный раунд/).selectOption(stored.document.rounds[0].id);
  await editorPanel.getByRole('checkbox').check();
  // Hold an in-flight autosave while the AI theme is appended to the working copy.
  let releaseSave, capturedSave;
  const saveGate = new Promise(resolve => { releaseSave = resolve; });
  const saveStarted = new Promise(resolve => { capturedSave = resolve; });
  await page.route('**/api/drafts/' + draft.card.id, async route => {
    if (route.request().method() === 'PUT') { capturedSave(); await saveGate; }
    await route.continue();
  });
  await page.getByLabel('Название', { exact: true }).fill('Несохранённое название автора');
  await Promise.race([saveStarted, new Promise((_, reject) => setTimeout(() => reject(Error('autosave not started')), 10000))]);
  try { await editorPanel.getByRole('button', { name: 'Добавить тему в пакет', exact: true }).click(); }
  finally { releaseSave(); }

  await until(async () => (await api('GET', '/drafts/' + draft.card.id)).document.rounds[0].themes.length === 2, 'appended theme not saved');
  const updated = await api('GET', '/drafts/' + draft.card.id);
  assert.deepEqual(updated.document.rounds[0].themes[0], stored.document.rounds[0].themes[0]);
  assert.equal(updated.document.name, 'Несохранённое название автора');
  assert.deepEqual(sourceRequests, []);
  for (const width of [390, 820, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `overflow ${width}`);
  }
  checkpoint = 'disabled presentation';
  // UI-only disabled response: production default is separately verified in Go config tests.
  await page.route('**/api/ai/status', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ enabled: false, provider: '', model: '', monthly_limit: 0, spent: 0, reserved: 0, remaining: 0 }) }));
  await page.reload();
  await page.getByTestId('ai-authoring').locator('summary').click();
  await page.getByTestId('ai-disabled').waitFor();
  assert.equal(await page.getByRole('button', { name: 'Рассчитать оценку', exact: true }).isDisabled(), true);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ ai: 'pass', fixture: true, ambiguous_retry: true, edited_private_draft: true, append_theme: true, in_flight_autosave_preserved: true, disabled_ui_mock: true, responsive: true, page_errors: 0 }));
} catch (error) {
  console.error({ checkpoint, errors, url: page.url(), message: error.message });
  throw error;
} finally {
  try { if (registered) await api('DELETE', '/account/me', { password }); }
  finally { await browser.close(); }
}
