// Successful boards are cached independently of requests currently in flight.
export class BoardLoader<T> {
  private loadedKey = '';
  private active: {
    key: string;
    controller: AbortController;
    promise: Promise<T | undefined>;
  } | null = null;

  get pending() {
    return this.active !== null;
  }

  load(
    key: string,
    fetchBoard: (signal: AbortSignal) => Promise<T>,
    force = false
  ): Promise<T | undefined> {
    if (this.active?.key === key) return this.active.promise;
    if (!force && this.loadedKey === key) {
      this.active?.controller.abort();
      this.active = null;
      return Promise.resolve(undefined);
    }
    this.active?.controller.abort();
    const request: NonNullable<typeof this.active> = {
      key,
      controller: new AbortController(),
      promise: Promise.resolve<T | undefined>(undefined)
    };
    this.active = request;
    request.promise = (async () => {
      try {
        const value = await fetchBoard(request.controller.signal);
        if (this.active !== request || request.controller.signal.aborted) return undefined;
        this.loadedKey = key;
        return value;
      } catch (error) {
        if (this.active === request && !request.controller.signal.aborted) throw error;
        return undefined;
      } finally {
        if (this.active === request) this.active = null;
      }
    })();
    return request.promise;
  }

  reset() {
    this.active?.controller.abort();
    this.active = null;
    this.loadedKey = '';
  }
}
