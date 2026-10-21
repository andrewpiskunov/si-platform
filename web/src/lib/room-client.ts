import { clearRoomCredentials, readRoomCredentials } from './room-credentials.ts';
import { api, APIError } from './api.ts';
import type { Appearance } from './appearance.ts';
import type { SessionView, V2Command } from './game-v2.ts';
import type { RoomSettings } from './types.ts';
export interface Participant {
  ready?: boolean;
  id: string;
  name: string;
  role: 'host' | 'player' | 'spectator';
  score: number;
  removed?: boolean;
}
export interface RoomState {
  room_id: string;
  seq: number;
  appearance?: Appearance;
  settings?: RoomSettings;
  gameplay?: SessionView;
  phase: 'running' | 'lobby' | 'open' | 'answering' | 'resolved' | 'paused' | 'finished';
  participants: Participant[];
  question?: { id: string; text: string; answer?: string; value: number };
  winner?: string;
  you: string;
}
export interface LegacyCommand {
  v: 1;
  id: string;
  type: string;
  question_id?: string;
  correct?: boolean;
}
export type Command = LegacyCommand | V2Command;
export interface Message {
  v: number;
  type: string;
  state?: RoomState;
  command_id?: string;
  code?: string;
  result_seq?: number;
  duplicate?: boolean;
}
export class RoomProtocol {
  token: string;
  state: RoomState | null = null;
  ready = false;
  revoked = false;
  terminalCode = '';
  appearanceEpoch = 0;
  pending = new Map<string, Command>();
  private known = new Map<string, string>();
  constructor(token: string) {
    this.token = token;
  }
  connected() {
    if (this.revoked) throw new Error(this.terminalCode || 'unauthorized');
    this.ready = false;
    return { v: 1, type: 'authenticate', token: this.token };
  }
  disconnected() {
    this.ready = false;
  }
  command(
    type: string,
    fields: { question_id?: string; correct?: boolean } = {},
    id: string = crypto.randomUUID()
  ): Command {
    if (!this.ready) throw new Error('disconnected');
    const command: Command = { v: 1, id, type, ...fields };
    const serialized = JSON.stringify(command);
    if (this.known.has(id) && this.known.get(id) !== serialized)
      throw new Error('command_conflict');
    if (!this.known.has(id)) {
      this.known.set(id, serialized);
      this.pending.set(id, command);
    }
    return command;
  }
  commandV2(command: V2Command): V2Command {
    if (!this.ready) throw new Error('disconnected');
    const serialized = JSON.stringify(command);
    if (this.known.has(command.id) && this.known.get(command.id) !== serialized)
      throw new Error('command_conflict');
    if (!this.known.has(command.id)) {
      this.known.set(command.id, serialized);
      this.pending.set(command.id, JSON.parse(serialized));
    }
    return command;
  }
  receive(message: Message): Command[] {
    if (this.revoked) return [];
    if (message.code === 'unauthorized' || message.code === 'room_expired') {
      this.revoked = true;
      this.terminalCode = message.code;
      this.ready = false;
      this.token = '';
      this.state = null;
      this.pending.clear();
      this.known.clear();
      return [];
    }
    if (message.v !== 1 && message.v !== 2) return [];
    if (message.type === 'appearance_refresh') {
      this.appearanceEpoch++;
      return [];
    }
    if (message.state && (!this.state || message.state.seq >= this.state.seq))
      this.state = message.state;
    if (
      message.command_id &&
      (message.type === 'result' ||
        (message.type === 'error' &&
          !['unavailable', 'ownership_lost'].includes(message.code || '')))
    )
      this.pending.delete(message.command_id);
    if (message.type === 'snapshot' && !this.ready) {
      this.ready = true;
      return [...this.pending.values()];
    }
    return [];
  }
}
export class RoomConnection {
  protocol: RoomProtocol;
  socket: WebSocket | null = null;
  private stopped = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private attempts = 0;
  private room: string;
  private update: (protocol: RoomProtocol, status: string, error?: string) => void;
  constructor(
    room: string,
    token: string,
    update: (protocol: RoomProtocol, status: string, error?: string) => void
  ) {
    this.room = room;
    this.protocol = new RoomProtocol(token);
    this.update = update;
  }
  private bootstrapping = false;
  async connect() {
    if (this.stopped || this.bootstrapping || this.socket?.readyState === WebSocket.OPEN) return;
    this.bootstrapping = true;
    this.update(this.protocol, 'connecting');
    try {
      await api(
        `/rooms/${encodeURIComponent(this.room)}/session`,
        'POST',
        {},
        this.protocol.token,
        AbortSignal.timeout(10000)
      );
    } catch (error) {
      if (this.stopped) return;
      if (
        error instanceof APIError &&
        (error.code === 'room_expired' || error.code === 'not_found')
      ) {
        this.expire();
        return;
      }
      if (error instanceof APIError && error.status === 401) {
        this.revoke();
        return;
      }
      if (!(error instanceof APIError && error.code === 'guest_session_mismatch')) {
        // Explicit retry only: bootstrap failure must not loop indefinitely.
        this.update(
          this.protocol,
          'failed',
          error instanceof Error ? error.message : 'unavailable'
        );
        return;
      }
    } finally {
      this.bootstrapping = false;
    }
    if (this.stopped) return;
    this.update(this.protocol, 'connecting');
    const url = new URL(`/api/rooms/${encodeURIComponent(this.room)}/ws`, location.origin);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(url);
    this.socket = socket;
    socket.onopen = () => {
      if (this.stopped || socket !== this.socket) {
        socket.close();
        return;
      }
      socket.send(JSON.stringify(this.protocol.connected()));
    };
    socket.onmessage = (event) => {
      if (this.stopped || socket !== this.socket) return;
      let message: Message;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message.code === 'room_expired') {
        this.expire();
        return;
      }
      if (message.code === 'unauthorized') {
        this.revoke();
        return;
      }
      for (const command of this.protocol.receive(message)) socket.send(JSON.stringify(command));
      if (message.type === 'snapshot') this.attempts = 0;
      this.update(
        this.protocol,
        this.protocol.ready ? 'connected' : 'connecting',
        message.code && message.code !== 'ok' ? message.code : undefined
      );
      if (
        message.type === 'error' &&
        ['unavailable', 'ownership_lost'].includes(message.code || '')
      )
        socket.close();
    };
    socket.onclose = () => {
      if (this.stopped || socket !== this.socket) return;
      this.protocol.disconnected();
      this.update(this.protocol, 'reconnecting');
      this.timer = setTimeout(() => this.connect(), Math.min(1000 * 2 ** this.attempts++, 15000));
    };
    socket.onerror = () => socket.close();
  }
  expire() {
    this.terminate('room_expired');
  }
  revoke() {
    this.terminate('unauthorized');
  }
  private terminate(code: 'unauthorized' | 'room_expired') {
    if (this.stopped) return;
    const token = this.protocol.token;
    const saved = readRoomCredentials(this.room);
    const superseded = !!saved && saved.token !== token;
    this.protocol.receive({ v: 1, type: 'error', code });
    clearRoomCredentials(this.room, token);
    this.close();
    if (superseded) return;
    this.update(this.protocol, code === 'room_expired' ? 'expired' : 'revoked', code);
  }
  send(type: string, fields: { question_id?: string; correct?: boolean } = {}) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) throw new Error('disconnected');
    const command = this.protocol.command(type, fields);
    this.socket.send(JSON.stringify(command));
    this.update(this.protocol, 'connected');
  }
  sendV2(command: V2Command) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) throw new Error('disconnected');
    this.socket.send(JSON.stringify(this.protocol.commandV2(command)));
    this.update(this.protocol, 'connected');
  }
  close() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.socket?.close();
    this.protocol.disconnected();
  }
}
