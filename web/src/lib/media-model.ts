import type { Asset } from './types.ts';
export class ServerClock {
  private server = 0;
  private local = 0;
  observe(server: number, local: number) {
    this.server = server;
    this.local = local;
  }
  now(local: number) {
    return this.server + Math.max(0, local - this.local);
  }
}
export function playbackPlan(
  media: { start_at_ms: number; paused_position_ms: number; duration_ms: number },
  paused: boolean,
  serverNow: number,
  stageKind = 'present_question'
) {
  const started = media.start_at_ms > 0;
  const wait = started ? Math.max(0, media.start_at_ms - serverNow) : 0;
  const position = paused
    ? media.paused_position_ms
    : started
      ? Math.max(0, serverNow - media.start_at_ms)
      : 0;
  const milliseconds = media.duration_ms > 0 ? Math.min(media.duration_ms, position) : position;
  return {
    show:
      started &&
      !(paused && ['prepare_content', 'scheduled_content'].includes(stageKind)) &&
      (wait === 0 || (paused && position > 0)),
    play: started && wait === 0 && !paused,
    position: milliseconds / 1000,
    wait
  };
}
export class CurrentResource {
  private active: { key: string; controller: AbortController; signal: AbortSignal } | null = null;
  begin(key: string) {
    this.close();
    const controller = new AbortController();
    return (this.active = { key, controller, signal: controller.signal });
  }
  current(request: { signal: AbortSignal }) {
    return this.active?.signal === request.signal && !request.signal.aborted;
  }
  close() {
    this.active?.controller.abort();
    this.active = null;
  }
}
export function assetReference(asset: Asset): string {
  return asset.name.replace(/^(images|audio|video)\//i, '');
}
export function readyAssets(assets: Asset[], kind: string): Asset[] {
  return assets.filter(
    (asset) =>
      asset.prepared?.kind === kind &&
      !!asset.prepared.standard?.key &&
      !!asset.prepared.economy?.key
  );
}
export function findPrepared(assets: Asset[], kind: string, reference: string) {
  return readyAssets(assets, kind).find(
    (asset) => assetReference(asset) === reference || asset.name === reference
  );
}
export function preparedURL(packId: string, revision: number, key: string) {
  return `/api/drafts/${encodeURIComponent(packId)}/prepared/${encodeURIComponent(key)}?revision=${revision}`;
}
