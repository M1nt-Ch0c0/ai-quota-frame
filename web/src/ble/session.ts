import { check, concat, deadline, equal, hex, sleep, u32, utf8, view } from '../bytes.ts';
import type { Link } from './gatt-link.ts';
import type { StoredBond } from './bonds.ts';
export interface DeviceInfo { version: number; id: string; wifiState: number; ip: string; }
export interface Target { generation: number; revision: number; }
export class DeviceError extends Error {
  readonly code: number;
  constructor(code: number) { super(`设备拒绝请求（${code}）${code === 3 ? '，设备忙' : ''}`); this.code = code; }
}
export function parseInfo(data: Uint8Array): DeviceInfo {
  if (data.length !== 20 || ![1, 2].includes(data[0]) || data[1] > 3) throw new Error('设备发现信息不兼容');
  return { version: data[0], id: hex(data.subarray(2, 8)), wifiState: data[1], ip: Array.from(data.subarray(8, 12)).join('.') };
}
export type HmacKey = Uint8Array | CryptoKey;
export async function hmac(key: HmacKey, domain: string, data: Uint8Array): Promise<Uint8Array> {
  const imported = key instanceof CryptoKey ? key : await crypto.subtle.importKey('raw', key as BufferSource, { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  return new Uint8Array(await crypto.subtle.sign('HMAC', imported, concat(utf8(domain), data) as BufferSource));
}
export class Session {
  private sequence = 0;
  private dead = false;
  private running = false;
  readonly link: Link;
  private readonly key: Uint8Array;
  readonly info: DeviceInfo;
  readonly bindingID: string;
  private constructor(link: Link, key: Uint8Array, info: DeviceInfo, bindingID = '') { this.link = link; this.key = key; this.info = info; this.bindingID = bindingID; }
  static async authenticate(link: Link, token: string, expectedID: string, signal: AbortSignal, nonce?: Uint8Array): Promise<Session> {
    if (!/^[\x21-\x7e]{32,128}$/.test(token)) throw new Error('推图令牌须为 32–128 个无空白 ASCII 字符');
    try {
      const record = await link.info(signal); const info = parseInfo(record);
      if (info.version !== 1) throw new Error('此相框使用按键配对，请通过设备配对入口连接');
      if (info.id !== expectedID) throw new Error('设备身份与选择结果不一致');
      const challenge = await link.challenge(signal);
      if (challenge.length !== 32) throw new Error('设备鉴权挑战无效，请重新连接');
      const clientNonce = nonce ?? crypto.getRandomValues(new Uint8Array(32));
      if (clientNonce.length !== 32) throw new Error('客户端随机数长度错误');
      const transcript = concat(record.subarray(2, 8), challenge, clientNonce);
      const key = await hmac(utf8(token), 'pp-ble-v1/session', transcript);
      const proof = await hmac(utf8(token), 'pp-ble-v1/client', transcript);
      const client = new Session(link, key, info);
      const response = await link.exchange(concat(new Uint8Array([1]), clientNonce, proof), deadline(signal, 45000));
      const signed = parseInfo(await client.verify(1, 0, response));
      if (signed.id !== expectedID) throw new Error('设备签名身份不匹配');
      return new Session(link, key, signed);
    } catch (error) { link.disconnect(); throw error; }
  }
  static async authenticateBond(link: Link, bond: StoredBond, signal: AbortSignal, nonce?: Uint8Array): Promise<Session> {
    try {
      const record = await link.info(signal), info = parseInfo(record);
      if (info.version !== 2 || info.id !== bond.deviceID) throw new Error('相框身份与保存的配对关系不一致');
      const challenge = await link.challenge(signal);
      if (challenge.length !== 32) throw new Error('设备连接信息无效，请重新连接');
      const clientNonce = nonce ?? crypto.getRandomValues(new Uint8Array(32));
      if (clientNonce.length !== 32) throw new Error('客户端随机数长度错误');
      const cid = hexBytes(bond.clientID), bid = hexBytes(bond.bindingID);
      const transcript = concat(new Uint8Array([2]), record.subarray(2, 8), cid, bid, challenge, clientNonce);
      const proof = await hmac(bond.key, 'pp-bond-v2/client', transcript);
      const key = await hmac(bond.key, 'pp-bond-v2/session', transcript);
      const client = new Session(link, key, info, bond.bindingID);
      const response = await link.exchange(concat(new Uint8Array([23]), cid, bid, clientNonce, proof), deadline(signal, 45000));
      const signed = parseInfo(await client.verify(23, 0, response));
      if (signed.version !== 2 || signed.id !== bond.deviceID) throw new Error('相框配对身份校验失败');
      return new Session(link, key, signed, bond.bindingID);
    } catch (error) { link.disconnect(); throw error; }
  }
  private async verify(op: number, sequence: number, data: Uint8Array): Promise<Uint8Array> {
    if (data.length < 39 || data.length > 512 || data[0] !== op || view(data).getUint32(1, true) !== sequence ||
      !equal(await hmac(this.key, 'pp-ble-v1/response', data.subarray(0, -32)), data.subarray(-32))) {
      this.dead = true; this.link.disconnect(); throw new Error('设备签名校验失败，请检查配对关系并重新连接');
    }
    const code = view(data).getUint16(5, true);
    if (code) throw new DeviceError(code);
    return data.slice(7, -32);
  }
  async command(op: number, payload: Uint8Array, signal: AbortSignal, long = false): Promise<Uint8Array> {
    check(signal);
    if (this.dead || this.sequence === 0xffffffff || payload.length > 475) throw new Error('蓝牙会话已失效，请重新连接');
    if (this.running) throw new Error('同一会话不能并发请求');
    this.running = true;
    try {
      const sequence = ++this.sequence; const frame = concat(new Uint8Array([op]), u32(sequence), payload);
      const signed = concat(frame, await hmac(this.key, 'pp-ble-v1/request', frame));
      return await this.verify(op, sequence, await this.link.exchange(signed, long ? signal : deadline(signal, 45000)));
    } catch (error) {
      if (!(error instanceof DeviceError)) { this.dead = true; this.link.disconnect(); }
      throw error;
    } finally { this.running = false; }
  }
  async busyCommand(op: number, payload: Uint8Array, signal: AbortSignal, long = false): Promise<Uint8Array> {
    for (;;) {
      try { return await this.command(op, payload, signal, long); }
      catch (error) { if (!(error instanceof DeviceError) || error.code !== 3) throw error; await sleep(50, signal); }
    }
  }
  async status(signal: AbortSignal): Promise<Record<string, unknown>> {
    let result: Uint8Array = new Uint8Array(); let total = 0;
    for (;;) {
      const page = await this.command(3, u32(result.length), signal);
      if (page.length <= 8) throw new Error('设备状态分页不完整');
      const size = view(page).getUint32(0, true); const offset = view(page).getUint32(4, true);
      if (!size || size > 4095 || (total && size !== total) || offset !== result.length) throw new Error('设备状态分页不一致');
      total = size; result = concat(result, page.subarray(8));
      if (result.length > total) throw new Error('设备状态超出声明长度');
      if (result.length === total) {
        const parsed: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(result));
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('设备状态格式错误');
        return parsed as Record<string, unknown>;
      }
    }
  }
  async target(signal: AbortSignal): Promise<Target> { return targetFromStatus(await this.status(signal)); }
  disconnect() { this.dead = true; this.link.disconnect(); }
}
function hexBytes(value: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/.test(value)) throw new Error('本机配对身份格式错误');
  return Uint8Array.from(value.match(/../g)!, pair => parseInt(pair, 16));
}
export function targetFromStatus(state: Record<string, unknown>): Target {
  const integer = (v: unknown) => typeof v === 'number' && Number.isInteger(v) && v > 0 && v <= 0xffffffff;
  const apps = state.apps;
  const matched = Array.isArray(apps) && apps.some(app => app?.id === 'photoframe' && app.bank === state.loaded_app);
  if (!matched || state.runtime_ready !== true || !integer(state.generation) || !integer(state.ble_service_revision) ||
      state.ble_characteristics !== 2 || state.ble_service_uuid !== 'a4fe200015364c88a2ce32ecdaac0471') {
    throw new Error('相框应用未就绪。请先在控制台检查 photoframe；网页不会安装或切换应用');
  }
  return { generation: state.generation as number, revision: state.ble_service_revision as number };
}
