// Quota models and rendering rules live in Go. This file only loads that same
// implementation and moves typed values across its WASM boundary.
export interface FrameWindow { id: string; label: string; used_percent?: number; remaining_percent?: number; resets_at?: string; observed_at?: string; source: string; }
export interface FrameAccount {
  provider: string; name: string; plan?: string; status: string; stale?: boolean;
  last_fresh_at?: string; windows: FrameWindow[]; reset_cards?: { expires_at?: string }[];
  warning?: string; error?: string;
}
export interface FrameSnapshot {
  schema_version: 1; refreshed_at: string; data_updated_at: string; last_fresh_at?: string;
  next_refresh_at: string; stale: boolean; accounts: FrameAccount[];
  usage?: { today_tokens: number; today_cost: number; currency: string; source: string; days?: { date: string; tokens: number; cost: number }[]; };
  errors?: string[];
}
export interface FrameOptions {
  theme?: 'classic' | 'mint' | 'arcade' | 'pop' | 'mint-skin';
  providers?: string; timezone?: string; refreshSeconds?: number;
}
export const DEFAULT_FRAME_OPTIONS: Readonly<Required<FrameOptions>> = Object.freeze({
  theme: 'mint-skin', providers: 'claude:CLAUDE,codex:CODEX,xai:GROK', timezone: 'Asia/Shanghai', refreshSeconds: 900,
});
export interface ArtworkBounds { Left: number; Top: number; Right: number; Bottom: number; }
interface FrameBridge {
  fingerprint(input: string): string;
  html(input: string): string;
  quantize(png: Uint8Array, bounds: string): Uint8Array | string;
  collect(input: string): Promise<string>;
  cancel(id: string): void;
  reset(): void;
}
interface GoRuntime { importObject: WebAssembly.Imports; run(instance: WebAssembly.Instance): Promise<void>; }
declare global {
  var Go: { new(): GoRuntime } | undefined;
  var photoPainterFrameBridge: FrameBridge | undefined;
}
let boot: Promise<FrameBridge> | undefined;
const resource = (file: string) => new URL(file, new URL('.', location.href)).href;
async function loadRuntime(): Promise<FrameBridge> {
  if (globalThis.photoPainterFrameBridge) return globalThis.photoPainterFrameBridge;
  if (!globalThis.Go) {
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement('script'); script.src = resource('wasm_exec.js'); script.async = true;
      script.onload = () => resolve(); script.onerror = () => reject(new Error('无法加载相框引擎，请刷新页面重试'));
      document.head.appendChild(script);
    });
  }
  if (!globalThis.Go) throw new Error('相框引擎没有正确启动');
  const response = await fetch(resource('frame.wasm'), { credentials: 'omit', redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(60000) });
  if (!response.ok) throw new Error('无法加载相框引擎，请刷新页面重试');
  const runtime = new globalThis.Go();
  const { instance } = await WebAssembly.instantiate(await response.arrayBuffer(), runtime.importObject);
  let ended: unknown;
  void runtime.run(instance).catch(error => { ended = error; });
  for (let attempt = 0; attempt < 1000; attempt++) {
    if (globalThis.photoPainterFrameBridge) return globalThis.photoPainterFrameBridge;
    if (ended) throw new Error('相框引擎启动失败');
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  throw new Error('相框引擎启动超时，请刷新页面重试');
}
export function frameEngine(): Promise<FrameBridge> {
  return boot ??= loadRuntime().catch(error => { boot = undefined; throw error; });
}
function unwrap<T>(encoded: string): T {
  const result = JSON.parse(encoded) as { ok: boolean; value?: T; error?: string };
  if (!result.ok) throw new Error(result.error || '相框引擎未完成操作');
  return result.value as T;
}
export async function frameHTML(snapshot: FrameSnapshot, options: FrameOptions = {}): Promise<string> {
  return unwrap<string>((await frameEngine()).html(JSON.stringify({ snapshot, options })));
}
export async function frameFingerprint(snapshot: FrameSnapshot): Promise<string> {
  return unwrap<string>((await frameEngine()).fingerprint(JSON.stringify(snapshot)));
}
export async function quantizeFrame(png: Uint8Array, bounds: ArtworkBounds[]): Promise<Uint8Array> {
  const result = (await frameEngine()).quantize(png, JSON.stringify(bounds));
  if (typeof result === 'string') return unwrap<Uint8Array>(result);
  return result;
}
export async function collectSnapshot(managementKey: string, signal: AbortSignal, options: FrameOptions = {}): Promise<FrameSnapshot> {
  signal.throwIfAborted(); const api = await frameEngine(); signal.throwIfAborted();
  const id = crypto.randomUUID(), abort = () => api.cancel(id);
  signal.addEventListener('abort', abort, { once: true });
  try {
    signal.throwIfAborted();
    const result = await api.collect(JSON.stringify({ managementKey, options, id })); signal.throwIfAborted();
    return JSON.parse(result) as FrameSnapshot;
  } finally { signal.removeEventListener('abort', abort); }
}
export function forgetSourceCredentials(): void { globalThis.photoPainterFrameBridge?.reset(); }
