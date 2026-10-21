import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  RulesVersionObserver,
  createForm,
  editForm,
  observeForm,
  resetForm,
  submitForm,
  voiceDestination,
  validRoomSettings,
  normalizedSettings,
  roomListQuery
} from './room-preferences.ts';
import { classicRules, prepareAction, type SessionView } from './game-v2.ts';

function memory() {
  const values = new Map<string, string>();
  return () => ({
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    }
  });
}
test('rules version uses first snapshot as baseline and announces only a newer version across reconnect', () => {
  const observer = new RulesVersionObserver('room:player', memory());
  assert.equal(observer.observe(3), false);
  assert.equal(observer.observe(3), false);
  assert.equal(observer.observe(2), false);
  assert.equal(observer.observe(4), true);
  assert.equal(observer.observe(4), false);
  assert.equal(observer.observe(6), true);
});
test('rules observer retains the tab baseline on reload without mixing rooms or participants', () => {
  const storage = memory();
  new RulesVersionObserver('a:player', storage).observe(3);
  assert.equal(new RulesVersionObserver('a:player', storage).observe(3), false);
  assert.equal(new RulesVersionObserver('a:player', storage).observe(5), true);
  assert.equal(new RulesVersionObserver('b:player', storage).observe(5), false);
  assert.equal(new RulesVersionObserver('a:spectator', storage).observe(5), false);
});
test('rules observation still works in memory when storage getter/read/write is denied', () => {
  const denied = () => {
    throw new Error('denied');
  };
  const observer = new RulesVersionObserver('room:player', denied);
  assert.equal(observer.observe(1), false);
  assert.equal(observer.observe(2), true);
  assert.equal(observer.observe(2), false);
  const writesDenied = new RulesVersionObserver('other', () => ({
    getItem: () => '1',
    setItem: () => {
      throw new Error('quota');
    }
  }));
  assert.equal(writesDenied.observe(2), true);
  assert.equal(writesDenied.observe(NaN), false);
});
test('dirty rule edits survive incoming changed rules and explicit reset adopts the latest snapshot', () => {
  const initial = classicRules();
  let form = createForm(initial);
  form = editForm(form, { ...initial, answer_seconds: 27 });
  assert.equal(observeForm(form, { ...initial }), form);
  form = observeForm(form, { ...initial, penalties: false });
  assert.equal(form.value.answer_seconds, 27);
  assert.equal(form.conflict, true);
  assert.equal(form.dirty, true);
  form = resetForm(form);
  assert.equal(form.value.answer_seconds, 10);
  assert.equal(form.value.penalties, false);
  assert.equal(form.dirty, false);
  assert.equal(form.conflict, false);
  assert.deepEqual(initial, classicRules());
});
test('room settings submission keeps later edits when its accepted snapshot arrives', () => {
  let form = createForm({ visibility: 'private', voice_url: '' });
  form = editForm(form, { visibility: 'public', voice_url: '' });
  form = submitForm(form);
  form = editForm(form, { visibility: 'public', voice_url: 'https://voice.example/room' });
  form = observeForm(form, { visibility: 'public', voice_url: '' });
  assert.equal(form.value.voice_url, 'https://voice.example/room');
  assert.equal(form.dirty, true);
  assert.equal(form.conflict, false);
  form = submitForm(form);
  form = observeForm(form, { visibility: 'public', voice_url: 'https://voice.example/room' });
  assert.equal(form.dirty, false);
});
test('pristine forms follow the server and editing back to latest clears conflict', () => {
  let form = observeForm(createForm({ value: 1 }), { value: 2 });
  assert.equal(form.value.value, 2);
  form = editForm(form, { value: 3 });
  form = observeForm(form, { value: 4 });
  assert.equal(form.conflict, true);
  form = editForm(form, { value: 4 });
  assert.equal(form.dirty, false);
  assert.equal(form.conflict, false);
});
test('voice links accept only bounded explicit HTTPS destinations without credentials or unsafe whitespace', () => {
  assert.deepEqual(voiceDestination('https://voice.example/room?x=1'), {
    href: 'https://voice.example/room?x=1',
    hostname: 'voice.example'
  });
  for (const value of [
    '',
    'javascript:alert(1)',
    'http://voice.example',
    '//voice.example',
    'https:voice.example',
    'https://user:pw@voice.example',
    'https://@voice.example',
    'https://voice.example/a b',
    'https://voice.example/\\other',
    'https://voice.example/\n',
      'https://voice.example/\u0085',
    'https://voice.example/' + 'я'.repeat(1024)
  ]) {
    assert.equal(voiceDestination(value), null, value);
  }
  assert.equal(validRoomSettings({ visibility: 'private', voice_url: '' }), true);
  assert.equal(
    validRoomSettings({ visibility: 'public', voice_url: 'https://voice.example' }),
    true
  );
  assert.equal(validRoomSettings({ visibility: 'unknown', voice_url: '' }), false);
  assert.equal(validRoomSettings({ visibility: 'private' }), false);
  assert.equal(
    validRoomSettings({ visibility: 'private', voice_url: '', unexpected: true }),
    false
  );
  assert.deepEqual(normalizedSettings(undefined), { visibility: 'private', voice_url: '' });
});
test('public room query accepts only bounded cursor and limit and never sends appearance context', () => {
  const cursor = 'a'.repeat(32);
  assert.equal(
    roomListQuery(new URLSearchParams(`after=${cursor}&limit=12&room=private`)),
    `after=${cursor}&limit=12`
  );
  assert.equal(roomListQuery(new URLSearchParams()), 'limit=50');
  for (const query of [
    'after=../secret',
    'after=abc',
    'limit=0',
    'limit=51',
    'limit=1.5',
    'limit=word'
  ]) {
    assert.throws(() => roomListQuery(new URLSearchParams(query)), /invalid/);
  }
});
test('room settings command uses the existing v2 sequenced payload and rejects invalid settings', () => {
  const view = {
    version: 2,
    stage: { id: 'lobby', kind: 'lobby', expired: false },
    allowed_actions: [{ type: 'set_room_settings', requires_seq: true }]
  } as SessionView;
  const settings = { visibility: 'public', voice_url: 'https://voice.example' };
  assert.deepEqual(
    prepareAction(view, 7, 'set_room_settings', { settings }, undefined, 'command'),
    {
      v: 2,
      id: 'command',
      type: 'set_room_settings',
      expected_seq: 7,
      data: { settings }
    }
  );
  assert.throws(
    () => prepareAction(view, 7, 'set_room_settings', { settings: { visibility: 'public' } }),
    /invalid_action_data/
  );
});
