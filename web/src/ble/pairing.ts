import { check, concat, deadline, equal, hex, sha256, sleep, unhex, utf8, view } from '../bytes.ts';
import { DeviceError, hmac, parseInfo, Session } from './session.ts';
import type { Link } from './gatt-link.ts';
import { validateBond, type BondStore, type StoredBond } from './bonds.ts';

export const PAIRING_TIMEOUT = 15 * 60 * 1000;
export const PairState = { unbound: 0, reveal: 1, ready: 2, displaying: 3, confirm: 4, bound: 5, cancelled: 6, expired: 7, fault: 8 } as const;
export interface PairingInfo { state: number; bindingID: string; clientID: string; remainingMs: number; name: string; }
export interface PairingProgress {
  stage: 'negotiating' | 'saving' | 'displaying' | 'confirm-on-frame' | 'paired';
  deviceID: string;
  code?: string;
  remainingMs?: number;
}
export interface PairDeviceOptions { deviceID: string; browserID: string; name: string; clientName: string; }
export class PairingError extends Error {
  readonly kind: 'owned' | 'pending' | 'cancelled' | 'expired' | 'fault' | 'legacy';
  constructor(kind: PairingError['kind'], message: string) { super(message); this.kind = kind; }
}
const V = new Uint8Array([2]);
const nonzero = (data: Uint8Array) => data.some(v => v !== 0);
export function clientNameBytes(value: string): Uint8Array {
  const name = value.trim();
  if (!/^[\x20-\x7e]{1,32}$/.test(name)) throw new Error('配对名称须为 1–32 个英文字符');
  return utf8(name);
}
export async function clientCommit(d: Uint8Array, cid: Uint8Array, bid: Uint8Array, pub: Uint8Array, nonce: Uint8Array, name: Uint8Array): Promise<Uint8Array> {
  return sha256(concat(utf8('pp-pair-v2/client-commit'), V, d, cid, bid, pub, nonce, new Uint8Array([name.length]), name));
}
export async function serverCommit(d: Uint8Array, cid: Uint8Array, bid: Uint8Array, commit: Uint8Array, pub: Uint8Array, nonce: Uint8Array, name: Uint8Array): Promise<Uint8Array> {
  return sha256(concat(utf8('pp-pair-v2/server-commit'), V, d, cid, bid, commit, pub, nonce, new Uint8Array([name.length]), name));
}
export function pairingTranscript(d: Uint8Array, cid: Uint8Array, bid: Uint8Array, ccommit: Uint8Array, scommit: Uint8Array,
  cpub: Uint8Array, cnonce: Uint8Array, spub: Uint8Array, snonce: Uint8Array, name: Uint8Array): Uint8Array {
  return concat(V, d, cid, bid, ccommit, scommit, cpub, cnonce, spub, snonce, new Uint8Array([name.length]), name);
}
export async function deriveRoot(privateKey: CryptoKey, serverPublic: Uint8Array, transcript: Uint8Array): Promise<CryptoKey> {
  if (serverPublic.length !== 65 || serverPublic[0] !== 4) throw new Error('相框配对公钥无效');
  const publicKey = await crypto.subtle.importKey('raw', serverPublic as BufferSource, { name: 'ECDH', namedCurve: 'P-256' }, false, []);
  const shared = new Uint8Array(await crypto.subtle.deriveBits({ name: 'ECDH', public: publicKey }, privateKey, 256));
  const ikm = await crypto.subtle.importKey('raw', shared as BufferSource, 'HKDF', false, ['deriveBits']);
  shared.fill(0);
  const salt = await sha256(concat(utf8('pp-pair-v2/salt'), transcript));
  const bytes = new Uint8Array(await crypto.subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt: salt as BufferSource, info: utf8('pp-pair-v2/bond') as BufferSource }, ikm, 256));
  const key = await crypto.subtle.importKey('raw', bytes, { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  bytes.fill(0); return key;
}
export async function comparisonCode(key: CryptoKey, transcript: Uint8Array): Promise<string> {
  return String(view(await hmac(key, 'pp-pair-v2/code', transcript)).getUint32(0, true) % 1000000).padStart(6, '0');
}
function bootstrap(op: number, reply: Uint8Array): Uint8Array {
  if (reply.length < 7 || reply.length > 512 || reply[0] !== op || view(reply).getUint32(1, true) !== 0) throw new Error('相框配对回复无效');
  const code = view(reply).getUint16(5, true); if (code) throw new DeviceError(code);
  return reply.subarray(7);
}
/** Public metadata only. It must never be treated as a successful pairing. */
export async function readPairingStatus(link: Link, signal: AbortSignal): Promise<PairingInfo> {
  const p = bootstrap(18, await link.exchange(new Uint8Array([18]), deadline(signal, 20000)));
  if (p.length < 39 || p[0] !== 2 || p[1] > 8 || p[38] > 32 || p.length !== 39 + p[38]) throw new Error('相框配对状态无效');
  const name = new TextDecoder('utf-8', { fatal: true }).decode(p.subarray(39));
  if (name && !/^[\x20-\x7e]{1,32}$/.test(name)) throw new Error('相框配对名称无效');
  return { state: p[1], bindingID: hex(p.subarray(2, 18)), clientID: hex(p.subarray(18, 34)), remainingMs: view(p).getUint32(34, true), name };
}
export interface VerifiedPairState { state: number; remainingMs: number; code: number; }
export async function pairControl(link: Link, op: 21 | 22, bond: StoredBond, challenge: Uint8Array, signal: AbortSignal): Promise<VerifiedPairState> {
  const bid = unhex(bond.bindingID, 16), cid = unhex(bond.clientID, 16), d = unhex(bond.deviceID, 6);
  const proof = await hmac(bond.key, op === 21 ? 'pp-pair-v2/poll' : 'pp-pair-v2/cancel', concat(V, d, cid, bid, challenge));
  const reply = await link.exchange(concat(new Uint8Array([op]), bid, proof), deadline(signal, 20000));
  if (reply.length !== 93 || reply[0] !== op || view(reply).getUint32(1, true) !== 0 ||
    !equal(await hmac(bond.key, 'pp-pair-v2/response', reply.subarray(0, 61)), reply.subarray(61))) throw new Error('配对确认签名无效，请重新连接同一相框');
  const p = reply.subarray(7, 61);
  if (p[0] !== 2 || !equal(p.subarray(1, 17), bid) || p[17] > 8 || !equal(p.subarray(22, 54), challenge)) throw new Error('配对确认与当前连接不匹配');
  return { state: p[17], remainingMs: view(p).getUint32(18, true), code: view(reply).getUint16(5, true) };
}
export async function connectPaired(link: Link, bond: StoredBond, store: BondStore, signal: AbortSignal): Promise<Session> {
  validateBond(bond);
  const session = await Session.authenticateBond(link, bond, signal);
  if (bond.phase === 'pending') {
    try { await store.confirm(bond); } catch (error) { session.disconnect(); throw error; }
  }
  return session;
}
async function waitForApproval(link: Link, bond: StoredBond, store: BondStore, challenge: Uint8Array, signal: AbortSignal,
  onProgress: (progress: PairingProgress) => void, cancelSignal?: AbortSignal): Promise<Session> {
  for (;;) {
    check(signal);
    if (cancelSignal?.aborted) {
      // A user cancellation is serialized after the current GATT exchange.
      // If the physical approval won the race, preserve and prove that bond.
      let cancelled: VerifiedPairState;
      try { cancelled = await pairControl(link, 22, bond, challenge, signal); }
      catch {
        // The host may already have committed its owner and return an
        // unsigned STATE error to CANCEL. That error proves nothing. A new
        // BondAuth can prove approval without arming a cancelled candidate.
        try {
          const client = await connectPaired(link, bond, store, signal);
          onProgress({ stage: 'paired', deviceID: bond.deviceID }); return client;
        } catch { throw new PairingError('pending', '未确认取消结果，请重新连接同一相框'); }
      }
      if (cancelled.state === PairState.bound) {
        const client = await connectPaired(link, bond, store, signal);
        onProgress({ stage: 'paired', deviceID: bond.deviceID }); return client;
      }
      if (cancelled.code !== 0 || cancelled.state !== PairState.cancelled) throw new PairingError('pending', '未确认取消结果，请重新连接同一相框');
      await store.discardCandidate(bond);
      throw new PairingError('cancelled', '配对已取消');
    }
    // The first poll also proves that the candidate is durably stored before
    // the device arms its physical confirmation screen.
    const state = await pairControl(link, 21, bond, challenge, signal);
    if (state.state === PairState.cancelled || state.state === PairState.expired) {
      await store.discardCandidate(bond);
      throw new PairingError(state.state === PairState.expired ? 'expired' : 'cancelled', state.state === PairState.expired ? '配对请求已过期，请重新发起' : '配对已取消');
    }
    if (state.code) throw new DeviceError(state.code);
    if (state.state === PairState.bound) {
      const client = await connectPaired(link, bond, store, signal);
      onProgress({ stage: 'paired', deviceID: bond.deviceID }); return client;
    }
    if (![PairState.ready, PairState.displaying, PairState.confirm].includes(state.state as 2 | 3 | 4)) throw new PairingError('pending', '配对结果尚待确认，请重新连接同一相框');
    onProgress({ stage: state.state === PairState.confirm ? 'confirm-on-frame' : 'displaying', deviceID: bond.deviceID, code: bond.pairCode, remainingMs: state.remainingMs });
    await sleep(500, signal);
  }
}
export async function pairDevice(link: Link, options: PairDeviceOptions, store: BondStore, signal: AbortSignal,
  onProgress: (progress: PairingProgress) => void, cancelSignal?: AbortSignal): Promise<Session> {
  const operation = deadline(signal, PAIRING_TIMEOUT), name = clientNameBytes(options.clientName);
  let durable = false;
  try {
    if (cancelSignal?.aborted) throw new PairingError('cancelled', '配对已取消');
    onProgress({ stage: 'negotiating', deviceID: options.deviceID });
    const record = await link.info(operation), info = parseInfo(record);
    if (info.id !== options.deviceID) throw new Error('相框身份发生变化，请重新选择');
    if (info.version !== 2) throw new PairingError('legacy', '请先更新相框，以使用按键配对');
    const challenge = await link.challenge(operation);
    if (challenge.length !== 32) throw new Error('相框连接信息无效');
    const state = await readPairingStatus(link, operation), saved = await store.get(info.id);
    if (saved && state.bindingID === saved.bindingID && state.clientID === saved.clientID) {
      durable = saved.phase === 'pending';
      if (state.state === PairState.bound) return await connectPaired(link, saved, store, operation);
      if (saved.phase === 'pending' && [2, 3, 4, 6, 7].includes(state.state)) return await waitForApproval(link, saved, store, challenge, operation, onProgress, cancelSignal);
    }
    if (state.state === PairState.bound) throw new PairingError('owned', '相框已连接其他客户端，请在相框上长按按键重置配对');
    if (state.state === PairState.fault) throw new PairingError('fault', '相框配对状态需要重置，请长按相框按键');
    if (![0, 6, 7].includes(state.state) || saved?.phase === 'pending') throw new PairingError('pending', '有一项配对尚待确认，请重新连接同一相框恢复');
    const bid = challenge.slice(0, 16);
    if (!nonzero(bid) || (state.state === PairState.unbound && state.bindingID !== hex(bid))) throw new Error('相框配对身份无效');
    const cid = crypto.getRandomValues(new Uint8Array(16)), cnonce = crypto.getRandomValues(new Uint8Array(32));
    if (!nonzero(cid)) throw new Error('无法生成配对身份，请重试');
    const keys = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
    const cpub = new Uint8Array(await crypto.subtle.exportKey('raw', keys.publicKey));
    const d = record.subarray(2, 8), ccommit = await clientCommit(d, cid, bid, cpub, cnonce, name);
    const p = bootstrap(19, await link.exchange(concat(new Uint8Array([19]), cid, bid, ccommit, new Uint8Array([name.length]), name), deadline(operation, 20000)));
    if (p.length !== 114 || p[0] !== 2 || !equal(p.subarray(1, 17), bid)) throw new Error('相框配对承诺无效');
    const spub = p.slice(17, 82), scommit = p.slice(82);
    const reveal = await link.exchange(concat(new Uint8Array([20]), bid, cpub, cnonce), deadline(operation, 20000));
    if (reveal.length !== 125 || reveal[0] !== 20 || view(reveal).getUint32(1, true) !== 0) throw new Error('相框配对验证回复无效');
    const rp = reveal.subarray(7, 93);
    if (rp[0] !== 2 || !equal(rp.subarray(1, 17), bid) || !equal(rp.subarray(54, 86), challenge)) throw new Error('相框配对验证与当前连接不匹配');
    const snonce = rp.slice(17, 49);
    if (!equal(await serverCommit(d, cid, bid, ccommit, spub, snonce, name), scommit)) throw new Error('相框配对承诺校验失败');
    const transcript = pairingTranscript(d, cid, bid, ccommit, scommit, cpub, cnonce, spub, snonce, name);
    const key = await deriveRoot(keys.privateKey, spub, transcript);
    if (!equal(await hmac(key, 'pp-pair-v2/response', reveal.subarray(0, 93)), reveal.subarray(93))) throw new Error('相框配对签名校验失败');
    const result = view(reveal).getUint16(5, true); if (result) throw new DeviceError(result);
    if (rp[49] !== PairState.ready) throw new Error('相框尚未准备好确认配对');
    const bond: StoredBond = { schema: 1, deviceID: info.id, browserID: options.browserID, name: options.name.slice(0, 80),
      clientID: hex(cid), bindingID: hex(bid), createdAt: new Date().toISOString(), phase: 'pending', pairCode: await comparisonCode(key, transcript), key };
    onProgress({ stage: 'saving', deviceID: bond.deviceID });
    await store.putCandidate(bond); durable = true; check(operation);
    return await waitForApproval(link, bond, store, challenge, operation, onProgress, cancelSignal);
  } catch (error) {
    link.disconnect();
    if (durable && !(error instanceof PairingError)) throw new PairingError('pending', '配对结果尚待确认，请重新连接同一相框恢复');
    throw error;
  }
}
export async function cancelPairing(link: Link, bond: StoredBond, store: BondStore, signal: AbortSignal): Promise<void> {
  if (bond.phase !== 'pending') throw new PairingError('owned', '已配对的关系只能在相框上重置');
  const challenge = await link.challenge(signal);
  if (challenge.length !== 32) throw new Error('相框连接信息无效');
  let state: VerifiedPairState;
  try { state = await pairControl(link, 22, bond, challenge, signal); }
  catch {
    let proved: Session;
    try { proved = await connectPaired(link, bond, store, signal); }
    catch { throw new PairingError('pending', '未确认取消结果，请重新连接同一相框'); }
    proved.disconnect();
    throw new PairingError('owned', '相框已经同意配对，可以直接连接');
  }
  if (state.code || state.state !== PairState.cancelled) throw new PairingError('pending', '未确认取消结果，请重新连接同一相框');
  await store.discardCandidate(bond);
}
export async function grantedDevices(): Promise<BluetoothDevice[]> {
  if (!navigator.bluetooth?.getDevices) return [];
  return navigator.bluetooth.getDevices();
}
