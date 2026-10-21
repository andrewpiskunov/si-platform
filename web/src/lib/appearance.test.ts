import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  manifestStyle,
  roomContext,
  contextLink,
  themePreferences,
  checkAssetBudget,
  AppearanceResources
} from './appearance.ts';
import { RoomProtocol } from './room-client.ts';
const room = 'a'.repeat(32);
const manifest = {
  schema_version: 1,
  name: 'Вечер',
  author: 'Автор',
  provenance: 'Свои ресурсы',
  font: 'sans',
  palette: {
    background: '#101725',
    surface: '#192234',
    surface_alt: '#202d43',
    border: '#8899AA',
    text: '#FFFFFF',
    muted: '#CCDDEE',
    accent: '#EFBC6E',
    accent_text: '#211C16',
    danger: '#FFA2A5',
    success: '#91DBBC',
    focus: '#FFFFFF'
  },
  cards: { radius: 12, border_width: 2 },
  buttons: { radius: 8, border_width: 1 }
};
test('theme CSS contains only validated palette, platform fonts and bounded shape variables', () => {
  const extended = {
    ...manifest,
    css: 'body{display:none}',
    background: 'https://evil.test/a'
  };
  const style = manifestStyle(extended);
  assert.match(style, /--bg:#101725/);
  assert.match(style, /--card-radius:12px/);
  assert.doesNotMatch(style, /evil|display:none|background-image/);
  assert.throws(() =>
    manifestStyle({ ...manifest, palette: { ...manifest.palette, accent: 'red;position:fixed' } })
  );
  assert.throws(() => manifestStyle({ ...manifest, buttons: { radius: 999, border_width: 1 } }));
  assert.throws(() => manifestStyle({ ...manifest, font: 'url(https://evil.test)' }));
});
test('room context is explicit and navigation preserves filters without leaking into another room', () => {
  assert.equal(roomContext(new URL('https://site.test/rooms/' + room)), room);
  assert.equal(roomContext(new URL('https://site.test/?room=' + room)), room);
  assert.equal(roomContext(new URL('https://site.test/')), '');
  assert.equal(
    contextLink('/?q=кино#catalogue', room),
    '/?q=%D0%BA%D0%B8%D0%BD%D0%BE&room=' + room + '#catalogue'
  );
  assert.equal(contextLink('/rooms/' + 'b'.repeat(32), room), '/rooms/' + 'b'.repeat(32));
  assert.equal(contextLink('/studio', ''), '/studio');
});
test('local preferences remain per-room and mobile uses economy until explicitly overridden', () => {
  assert.deepEqual(themePreferences(null, true), { classic: false, economy: true });
  assert.deepEqual(themePreferences('{"classic":true,"economy":false}', true), {
    classic: true,
    economy: false
  });
  assert.deepEqual(themePreferences('broken', false), { classic: false, economy: false });
});
test('asset budgets include the whole chosen set and reject missing manifest assets', () => {
  const assets = [
    {
      index: 0,
      name: 'assets/bg.png',
      width: 10,
      height: 10,
      standard_size: 1500000,
      economy_size: 600000
    },
    {
      index: 1,
      name: 'assets/logo.png',
      width: 10,
      height: 10,
      standard_size: 1500000,
      economy_size: 600000
    }
  ];
  assert.throws(() =>
    checkAssetBudget(assets, 'standard', { ...manifest, background: 'assets/bg.png' })
  );
  assert.throws(() => checkAssetBudget(assets, 'economy', manifest));
  assert.throws(() =>
    checkAssetBudget([], 'standard', { ...manifest, logo: 'assets/missing.png' })
  );
  assert.equal(
    checkAssetBudget(assets.slice(0, 1), 'economy', { ...manifest, background: 'assets/bg.png' }),
    600000
  );
});
test('stale async theme completion cannot replace another context and releases its blobs', () => {
  const revoked: string[] = [];
  const resources = new AppearanceResources((url) => revoked.push(url));
  const first = resources.begin();
  const second = resources.begin();
  assert.equal(first.signal.aborted, true);
  assert.equal(resources.commit(second, ['blob:new']), true);
  assert.equal(resources.commit(first, ['blob:old']), false);
  assert.deepEqual(revoked, ['blob:old']);
  resources.clear();
  assert.deepEqual(revoked, ['blob:old', 'blob:new']);
});
test('appearance refresh increments its epoch without altering game state or pending commands', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({
    v: 1,
    type: 'snapshot',
    state: { room_id: room, seq: 3, phase: 'open', participants: [], you: 'p' }
  });
  protocol.command('buzz', { question_id: 'q' }, 'cmd');
  const state = protocol.state;
  const epoch = protocol.appearanceEpoch;
  assert.deepEqual(protocol.receive({ v: 1, type: 'appearance_refresh' }), []);
  assert.equal(protocol.appearanceEpoch, epoch + 1);
  assert.equal(protocol.state, state);
  assert.equal(protocol.pending.size, 1);
  assert.equal(protocol.ready, true);
});

test('image download aborts before reading bytes beyond its declared theme budget', async () => {
  const { readThemeImage } = await import('./appearance.ts');
  const response = new Response(
    new ReadableStream({
      start(controller) {
        controller.enqueue(new Uint8Array(4));
        controller.enqueue(new Uint8Array(4));
        controller.close();
      }
    }),
    { headers: { 'Content-Type': 'image/png' } }
  );
  await assert.rejects(() => readThemeImage(response, 5), /theme_budget/);
  const valid = await readThemeImage(
    new Response(new Uint8Array(5), { headers: { 'Content-Type': 'image/png' } }),
    5
  );
  assert.equal(valid.size, 5);
  await assert.rejects(
    () =>
      readThemeImage(new Response('<svg/>', { headers: { 'Content-Type': 'image/svg+xml' } }), 6),
    /invalid_theme/
  );
});
