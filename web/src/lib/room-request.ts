export interface RoomRequest {
  room: string;
  token: string;
  signal: AbortSignal;
}
// One component's latest request, scoped to both room and admitted identity.
export class RoomRequestScope {
  private controller: AbortController | undefined;
  private disposed = false;
  begin(room: string, token: string): RoomRequest {
    this.controller?.abort();
    this.controller = new AbortController();
    if (this.disposed) this.controller.abort();
    return { room, token, signal: this.controller.signal };
  }
  current(
    request: RoomRequest,
    identity: () => { room: string | null | undefined; token: string | null | undefined }
  ): boolean {
    // Check lifecycle before evaluating Svelte props: their getters may refer to a
    // parent identity that has already been cleared during component destruction.
    if (this.disposed || request.signal.aborted || request.signal !== this.controller?.signal)
      return false;
    const current = identity();
    return request.room === current.room && request.token === current.token;
  }
  dispose() {
    this.disposed = true;
    this.controller?.abort();
  }
}
