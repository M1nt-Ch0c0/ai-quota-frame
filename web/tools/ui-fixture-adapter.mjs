// Explicitly test-only Bluetooth/transfer adapter. check-ui.mjs substitutes
// this module while bundling the real UI; production builds never import it.
const deviceID = '010203040506';
const browserID = 'local-ui-fixture';
const bindingID = '11111111111111111111111111111111';
const device = new EventTarget();
Object.assign(device, { id: browserID, name: 'PhotoPainter · UI fixture' });
const fixture = globalThis.__photoPainterUIFixture = {
  simulated: true, pickerCalls: 0, pairCalls: 0, reconnectCalls: 0,
  sends: [], pendingPair: undefined, pendingSend: undefined,
  approvePair() { this.pendingPair?.resolve(); },
  expirePair() { this.pendingPair?.reject(new PairingError('expired', '配对请求已过期，请重新发起')); },
  finishSend() { this.pendingSend?.resolve(); },
};
Object.defineProperty(navigator, 'bluetooth', { configurable: true, value: {
  requestDevice: async () => { fixture.pickerCalls++; return device; },
  getDevices: async () => [device],
} });
const label = document.createElement('div');
label.id = 'ui-fixture-label';
label.textContent = 'LOCAL UI TEST · PAIRING / TRANSFER SIMULATED';
label.style.cssText = 'position:fixed;right:6px;top:2px;z-index:2147483647;padding:2px 5px;background:#fff;color:#334155;border:1px solid #94a3b8;border-radius:3px;font:9px/1.4 ui-monospace,monospace;pointer-events:none';
document.body.append(label);

export const HOST_SERVICE = 'a4fe100015364c88a2ce32ecdaac0471';
export class GattLink {
  static async connect() { return new GattLink(); }
  async info() { const info = new Uint8Array(20); info.set([2, 0, 1, 2, 3, 4, 5, 6]); return info; }
  disconnect() {}
}
export class PairingError extends Error {
  constructor(kind, message) { super(message); this.kind = kind; }
}
const session = () => ({
  info: { id: deviceID, version: 2 }, bindingID,
  disconnect() {},
  async status() { return {
    version: 20, loaded_app: 1, apps: [{ id: 'photoframe', bank: 1 }],
    runtime_ready: true, generation: 1, ble_service_revision: 1,
    ble_characteristics: 2, ble_service_uuid: 'a4fe200015364c88a2ce32ecdaac0471',
  }; },
});
function deferred(signal) {
  let resolve, reject;
  const promise = new Promise((ok, fail) => { resolve = ok; reject = fail; });
  const abort = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
  signal.addEventListener('abort', abort, { once: true });
  return { resolve, reject, promise: promise.finally(() => signal.removeEventListener('abort', abort)) };
}
export async function grantedDevices() { return [device]; }
export async function connectPaired() { fixture.reconnectCalls++; return session(); }
export async function pairDevice(_link, options, store, signal, progress, cancelSignal) {
  fixture.pairCalls++;
  if ((await store.get(deviceID))?.phase === 'paired') return connectPaired();
  const key = await crypto.subtle.generateKey({ name: 'HMAC', hash: 'SHA-256', length: 256 }, false, ['sign']);
  const candidate = { schema: 1, ...options, clientID: '22222222222222222222222222222222',
    bindingID, pairCode: '482619', key, phase: 'pending', createdAt: new Date().toISOString() };
  await store.putCandidate(candidate);
  progress({ stage: 'displaying', deviceID, code: candidate.pairCode });
  progress({ stage: 'confirm-on-frame', deviceID, code: candidate.pairCode });
  const gate = deferred(AbortSignal.any([signal, cancelSignal]));
  fixture.pendingPair = gate;
  try {
    await gate.promise;
    progress({ stage: 'saving', deviceID, code: candidate.pairCode });
    await store.confirm(candidate);
    progress({ stage: 'paired', deviceID });
    return session();
  } catch (error) {
    await store.discardCandidate(candidate);
    throw error;
  } finally { fixture.pendingPair = undefined; }
}
export class PendingError extends Error {}
export class UnknownOutcomeError extends Error {}
export class Sender {
  async send(_session, bytes, at, signal, progress) {
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
    fixture.sends.push({ digest, bytes: bytes.length, at });
    progress('uploading', bytes.length, bytes.length);
    const gate = deferred(signal); fixture.pendingSend = gate;
    try { await gate.promise; progress('displaying', bytes.length, bytes.length); progress('complete', bytes.length, bytes.length); }
    finally { fixture.pendingSend = undefined; }
  }
  cancelUpload() { fixture.pendingSend?.reject(new Error('UI fixture upload cancelled')); }
  async recover() { throw new Error('No fixture transaction to recover'); }
}
