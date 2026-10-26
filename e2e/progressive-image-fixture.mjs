// Controlled component-browser fixture. This deliberately does NOT verify uploads,
// media processing, real room transport, or server media authorization/readiness.
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
const base = process.env.BASE_URL || 'http://localhost:5175';
const output = fileURLToPath(new URL('../.local/browser/', import.meta.url));
const directory = await mkdtemp(join(tmpdir(), 'ux13-image-'));
await mkdir(output, { recursive: true });
await promisify(execFile)(process.env.MEDIA_FFMPEG || 'ffmpeg', ['-v', 'error', '-nostdin', '-y', '-f', 'lavfi', '-i', 'testsrc=size=480x270:rate=1', '-frames:v', '1', join(directory, 'image.png')]);
const png = await readFile(join(directory, 'image.png'));
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' });
const page = await browser.newPage();
const errors = []; page.on('pageerror', error => errors.push(error.message));
const html = `<!DOCTYPE html><html><head><meta charset="utf-8"><style>body{font:18px sans-serif;background:#102653;color:white;margin:32px}#fixture{width:700px}img{max-height:360px}</style></head><body><h1>UX13 — controlled image renderer fixture</h1><p>Production MediaPlayer; synthetic snapshots and intercepted valid PNG. No real media processing.</p><div id="fixture"></div><script type="module">
import {mount,unmount} from '/node_modules/.vite/deps/svelte.js';
import MediaPlayer from '/src/lib/MediaPlayer.svelte';
let component;
window.renderFixture=async props=>{if(component)await unmount(component);component=mount(MediaPlayer,{target:document.querySelector('#fixture'),props:{...props,onsend:command=>window.fixtureCommands.push(command)}})};
window.fixtureCommands=[];
</script></body></html>`;
await page.route('**/ux13-component-fixture', route => route.fulfill({ contentType: 'text/html', body: html }));
await page.route('**/api/rooms/fixture/media-ticket', route => route.fulfill({ json: { url: base + '/ux13-fixture.png', expires_at: Date.now() + 600000 } }));
await page.route('**/ux13-fixture.png', route => route.fulfill({ contentType: 'image/png', body: png }));
const image = page.getByTestId('image-reveal');
async function render(kind, elapsed, paused = false, duration = 4000) {
  const now = Date.now(), start = now - elapsed;
  const media = { id: 'fixture-image', kind: 'image', standard: { size: png.length }, economy: { size: png.length }, start_at_ms: kind === 'prepare_content' ? 0 : start, paused_position_ms: elapsed, duration_ms: duration };
  const view = { version: 2, server_time_ms: now, rules: { progressive_reveal: true }, stage: { id: 'fixture-stage', instance_id: 'fixture-instance', kind }, question: { reveal: kind === 'present_question' ? { kind: 'image', start_at_ms: start, elapsed_ms: elapsed, duration_ms: duration } : undefined }, allowed_actions: [], players: [] };
  await page.evaluate(props => window.renderFixture(props), { media, view, seq: 1, you: 'viewer', roomID: 'fixture', token: 'fixture', connected: true, paused });
  await image.waitFor({ state: 'attached' });
  await page.waitForFunction(() => { const i = document.querySelector('[data-testid="image-reveal"]'); return i?.complete && i.naturalWidth > 0; });
  await page.waitForTimeout(130);
}
try {
  await page.goto(base + '/ux13-component-fixture');
  await page.waitForFunction(() => !!window.renderFixture);
  for (const kind of ['prepare_content', 'scheduled_content']) {
    // The scheduled start is deliberately already in the past: only authoritative
    // present_question may expose a progressive image, even after local clock passes it.
    await render(kind, 1000);
    assert.equal(Number(await image.getAttribute('data-progress')), 0, kind + ' must fully clip image');
    assert.ok(await image.evaluate(e => getComputedStyle(e).visibility === 'hidden' || getComputedStyle(e).clipPath.includes('100%')), kind + ' must hide image');
  }
  await render('present_question', 1000);
  assert.equal(await image.evaluate(e => getComputedStyle(e).visibility), 'visible');
  assert.ok(Number(await image.getAttribute('data-progress')) > .25);
  assert.ok(Number(await image.getAttribute('data-progress')) < .5);
  assert.match(await image.evaluate(e => getComputedStyle(e).clipPath), /^inset\(/);
  const progress = Number(await image.getAttribute('data-progress'));
  await page.waitForTimeout(400);
  assert.ok(Number(await image.getAttribute('data-progress')) > progress, 'image did not progressively unmask');
  await render('present_question', 1600, true);
  assert.equal(Number(await image.getAttribute('data-progress')), .4);
  const clip = await image.evaluate(e => getComputedStyle(e).clipPath);
  await page.waitForTimeout(400);
  assert.equal(await image.evaluate(e => getComputedStyle(e).clipPath), clip, 'paused image mask changed');
  await page.screenshot({ path: output + 'ux13-controlled-image-paused.png' });
  await page.reload();
  await page.waitForFunction(() => !!window.renderFixture);
  await render('present_question', 1600, true);
  assert.equal(Number(await image.getAttribute('data-progress')), .4, 'restored paused snapshot changed mask');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await render('present_question', 1600, true);
  assert.equal(Number(await image.getAttribute('data-progress')), .25, 'reduced motion should use quarter step behind current progress');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await render('present_question', 4000, true);
  assert.equal(Number(await image.getAttribute('data-progress')), 1);
  assert.equal(await image.evaluate(e => getComputedStyle(e).clipPath), 'none');
  await page.screenshot({ path: output + 'ux13-controlled-image-complete.png' });
  assert.deepEqual(errors, [], 'component browser runtime errors');
  console.log('PASS UX13 CONTROLLED image renderer: real production component + valid PNG; prepare/scheduled concealment, timed mask, pause/restored snapshot, reduced motion and full reveal. NOT real media upload/processing/room transport.');
} finally { await browser.close(); await rm(directory, { recursive: true, force: true }); }
