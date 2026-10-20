export interface Participant {
  id: string;
  name: string;
  role: 'host' | 'player' | 'spectator';
  score: number;
}
export interface RoomState {
  room_id: string;
  seq: number;
  phase: 'lobby' | 'open' | 'answering' | 'resolved' | 'paused' | 'finished';
  participants: Participant[];
  question?: { id: string; text: string; answer?: string; value: number };
  winner?: string;
  you: string;
}
export interface Command {
  v: 1;
  id: string;
  type: string;
  question_id?: string;
  correct?: boolean;
}
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
  pending = new Map<string, Command>();
  private known = new Map<string, string>();
  constructor(token: string) {
    this.token = token;
  }
  connected() {
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
  receive(message: Message): Command[] {
    if (message.v !== 1) return [];
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
  connect() {
    if (this.stopped) return;
    this.update(this.protocol, 'connecting');
    const url = new URL(`/api/rooms/${encodeURIComponent(this.room)}/ws`, location.origin);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(url);
    this.socket = socket;
    socket.onopen = () => socket.send(JSON.stringify(this.protocol.connected()));
    socket.onmessage = (event) => {
      let message: Message;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      for (const command of this.protocol.receive(message)) socket.send(JSON.stringify(command));
      if (message.type === 'snapshot') this.attempts = 0;
      this.update(
        this.protocol,
        this.protocol.ready ? 'connected' : 'connecting',
        message.code && message.code !== 'ok' ? message.code : undefined
      );
      if (message.code === 'unauthorized') this.close();
      else if (
        message.type === 'error' &&
        ['unavailable', 'ownership_lost'].includes(message.code || '')
      )
        socket.close();
    };
    socket.onclose = () => {
      this.protocol.disconnected();
      if (this.stopped) return;
      this.update(this.protocol, 'reconnecting');
      this.timer = setTimeout(() => this.connect(), Math.min(1000 * 2 ** this.attempts++, 15000));
    };
    socket.onerror = () => socket.close();
  }
  send(type: string, fields: { question_id?: string; correct?: boolean } = {}) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) throw new Error('disconnected');
    const command = this.protocol.command(type, fields);
    this.socket.send(JSON.stringify(command));
    this.update(this.protocol, 'connected');
  }
  close() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.socket?.close();
    this.protocol.disconnected();
  }
}
