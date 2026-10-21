import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RoomProtocol } from './room-client.ts';
const state = (seq: number) => ({
  room_id: 'room',
  seq,
  phase: 'open' as const,
  participants: [],
  you: 'me'
});
test('reconnect authenticates before replaying the exact unacknowledged command', () => {
  const protocol = new RoomProtocol('token');
  assert.deepEqual(protocol.connected(), { v: 1, type: 'authenticate', token: 'token' });
  protocol.receive({ v: 1, type: 'snapshot', state: state(2) });
  const command = protocol.command('buzz', { question_id: 'q' }, 'attempt-1');
  protocol.disconnected();
  protocol.connected();
  assert.equal(protocol.ready, false);
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(4) }), [command]);
  assert.deepEqual(command, { v: 1, id: 'attempt-1', type: 'buzz', question_id: 'q' });
});
test('an older acknowledgement clears pending without replacing a newer snapshot', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(8) });
  protocol.command('buzz', { question_id: 'q' }, 'attempt-1');
  protocol.receive({
    v: 1,
    type: 'result',
    command_id: 'attempt-1',
    code: 'ok',
    result_seq: 3,
    duplicate: true,
    state: state(3)
  });
  assert.equal(protocol.state?.seq, 8);
  assert.equal(protocol.pending.size, 0);
  protocol.disconnected();
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(9) }), []);
});
test('same command ID cannot silently change its payload', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(1) });
  const first = protocol.command('judge', { question_id: 'q', correct: true }, 'judge-1');
  assert.deepEqual(
    protocol.command('judge', { question_id: 'q', correct: true }, 'judge-1'),
    first
  );
  assert.throws(
    () => protocol.command('judge', { question_id: 'q', correct: false }, 'judge-1'),
    /command_conflict/
  );
});
test('transport failure keeps an uncertain command available for retry', () => {
  const protocol = new RoomProtocol('token');
  protocol.receive({ v: 1, type: 'snapshot', state: state(1) });
  const command = protocol.command('pause', {}, 'pause-1');
  protocol.receive({ v: 1, type: 'error', command_id: 'pause-1', code: 'unavailable' });
  protocol.disconnected();
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(2) }), [command]);
});
test('terminal unauthorized destroys credentials and pending commands and rejects later snapshots', () => {
  const protocol = new RoomProtocol('secret');
  protocol.receive({ v: 1, type: 'snapshot', state: state(1) });
  protocol.command('pause', {}, 'pending');
  protocol.receive({ v: 1, type: 'error', code: 'unauthorized' });
  assert.equal(protocol.token, '');
  assert.equal(protocol.pending.size, 0);
  assert.equal(protocol.state, null);
  assert.equal(protocol.ready, false);
  assert.deepEqual(protocol.receive({ v: 1, type: 'snapshot', state: state(2) }), []);
  assert.equal(protocol.state, null);
  assert.throws(() => protocol.connected(), /unauthorized/);
});
test('bootstrap precedes socket; revocation clears stored credential and prevents reconnect', async () => {
  const { RoomConnection } = await import('./room-client.ts');
  const { saveRoomCredentials, readRoomCredentials } = await import('./room-credentials.ts');
  const originals = new Map(
    ['fetch', 'WebSocket', 'location'].map((key) => [
      key,
      Object.getOwnPropertyDescriptor(globalThis, key)
    ])
  );
  const sockets: FakeSocket[] = [];
  class FakeSocket {
    static OPEN = 1;
    readyState = 1;
    onopen?: () => void;
    onmessage?: (event: { data: string }) => void;
    onclose?: () => void;
    onerror?: () => void;
    sent: string[] = [];
    url: URL;
    constructor(url: URL) {
      this.url = url;
      sockets.push(this);
    }
    send(data: string) {
      this.sent.push(data);
    }
    close() {
      this.readyState = 3;
      this.onclose?.();
    }
  }
  let complete!: (response: Response) => void;
  const statuses: string[] = [];
  Object.defineProperty(globalThis, 'fetch', {
    configurable: true,
    value: (url: string, init: RequestInit) => {
      assert.equal(url, '/api/rooms/bootstrap/session');
      assert.equal(init.method, 'POST');
      assert.equal((init.headers as Record<string, string>).Authorization, 'Bearer secret');
      return new Promise<Response>((resolve) => {
        complete = resolve;
      });
    }
  });
  Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: FakeSocket });
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { origin: 'http://localhost', protocol: 'http:' }
  });
  try {
    saveRoomCredentials({ room_id: 'bootstrap', participant_id: 'guest', token: 'secret' });
    const connection = new RoomConnection('bootstrap', 'secret', (_, status) =>
      statuses.push(status)
    );
    const connecting = connection.connect();
    assert.equal(sockets.length, 0);
    complete(new Response('{}'));
    await connecting;
    assert.equal(sockets.length, 1);
    sockets[0].onopen?.();
    assert.equal(sockets[0].url.search, '');
    sockets[0].onmessage?.({ data: JSON.stringify({ v: 1, type: 'snapshot', state: state(1) }) });
    connection.send('pause');
    sockets[0].onmessage?.({ data: JSON.stringify({ v: 1, type: 'error', code: 'unauthorized' }) });
    assert.equal(connection.protocol.pending.size, 0);
    assert.equal(readRoomCredentials('bootstrap'), null);
    assert.equal(statuses.at(-1), 'revoked');
    await connection.connect();
    assert.equal(sockets.length, 1);
    assert.throws(() => connection.send('pause'), /disconnected/);
  } finally {
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else Reflect.deleteProperty(globalThis, key);
    }
  }
});
test('bootstrap failures do not create sockets or schedule automatic retries; mismatch permits access', async () => {
  const { RoomConnection } = await import('./room-client.ts');
  const originals = new Map(
    ['fetch', 'WebSocket', 'location'].map((key) => [
      key,
      Object.getOwnPropertyDescriptor(globalThis, key)
    ])
  );
  let requests = 0,
    sockets = 0,
    status = '',
    code = 'unavailable';
  class FakeSocket {
    static OPEN = 1;
    readyState = 0;
    constructor() {
      sockets++;
    }
    close() {}
  }
  Object.defineProperty(globalThis, 'fetch', {
    configurable: true,
    value: async () => {
      requests++;
      return Response.json(
        { error: code },
        { status: code === 'unauthorized' ? 401 : code === 'guest_session_mismatch' ? 409 : 503 }
      );
    }
  });
  Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: FakeSocket });
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { origin: 'http://localhost', protocol: 'http:' }
  });
  try {
    const connection = new RoomConnection('failure', 'token', (_, value) => {
      status = value;
    });
    await connection.connect();
    assert.equal(sockets, 0);
    assert.equal(requests, 1);
    assert.equal(status, 'failed');
    assert.equal(connection.protocol.token, 'token');
    code = 'guest_session_mismatch';
    await connection.connect();
    assert.equal(sockets, 1);
    connection.close();
    code = 'unauthorized';
    const revoked = new RoomConnection('failure', 'token', (_, value) => {
      status = value;
    });
    await revoked.connect();
    assert.equal(status, 'revoked');
    assert.equal(revoked.protocol.token, '');
    await revoked.connect();
    assert.equal(requests, 3);
  } finally {
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else Reflect.deleteProperty(globalThis, key);
    }
  }
});
