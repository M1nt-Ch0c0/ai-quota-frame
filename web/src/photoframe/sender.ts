import { check, concat, deadline, equal, hex, sha256, sleep, u32, unhex, utf8, view } from '../bytes.ts';
import type { Target } from '../ble/session.ts';
import type { Pending, PendingStore } from './store.ts';
import { validatePNG } from '../png.ts';
export const IMAGE_TIMEOUT = 90 * 60 * 1000;
export interface ImageClient {
  info: { id: string };
  bindingID?: string;
  target(signal: AbortSignal): Promise<Target>;
  busyCommand(op: number, payload: Uint8Array, signal: AbortSignal, long?: boolean): Promise<Uint8Array>;
}
export interface Receipt { state: number; op: number; code: number; id: Uint8Array; size: number; received: number; digest: Uint8Array; }
export type Progress = (stage: 'uploading' | 'displaying' | 'complete', received: number, size: number) => void;
export class PendingError extends Error { constructor() { super('刷新结果待确认。重新连接同一设备后查询原回执；不会重复刷新'); } }
export class UnknownOutcomeError extends Error { constructor() { super('刷新结果不确定。请人工检查相框画面，网页不会自动重刷'); } }
export function parseReceipt(data: Uint8Array): Receipt {
  if (data.length !== 64 || !equal(data.subarray(0, 4), utf8('PFI1')) || data[4] > 7) throw new Error('相框业务回执格式错误');
  return { state: data[4], op: data[5], code: view(data).getUint16(6, true), id: data.slice(8, 24), size: view(data).getUint32(24, true), received: view(data).getUint32(28, true), digest: data.slice(32) };
}
function wire(op: number, record: Pending, tail: Uint8Array = new Uint8Array()): Uint8Array { return concat(new Uint8Array([1, op]), unhex(record.requestID, 16), tail); }
function targetBytes(target: Target, index: number): Uint8Array { return concat(u32(target.generation), u32(target.revision), u32(index)); }
export async function exchange(client: ImageClient, target: Target, command: Uint8Array, signal: AbortSignal, long = false): Promise<Receipt> {
  if (command.length > 128) throw new Error('图片命令超出 GATT 业务上限');
  await client.busyCommand(14, concat(targetBytes(target, 0), command), signal, long);
  const result = parseReceipt(await client.busyCommand(13, targetBytes(target, 1), signal));
  if (result.op !== command[1] || !equal(result.id, command.subarray(2, 18))) throw new Error('相框回执与当前请求不匹配');
  return result;
}
function matches(record: Pending, receipt: Receipt): boolean {
  return equal(receipt.id, unhex(record.requestID, 16)) && equal(receipt.digest, unhex(record.digest, 32)) && receipt.size === record.size && receipt.received <= record.size;
}
function requireReceiving(record: Pending, receipt: Receipt, offset: number): void {
  if (!matches(record, receipt) || receipt.state !== 1 || receipt.code !== 0 || receipt.received !== offset) throw new Error('相框未确认接收本次图片，请重新连接后重试');
}
export class Sender {
  private cancelRequested = false;
  private readonly store: PendingStore;
  private readonly random: (length: number) => Uint8Array;
  constructor(store: PendingStore, random = (length: number) => crypto.getRandomValues(new Uint8Array(length))) { this.store = store; this.random = random; }
  cancelUpload(): void { this.cancelRequested = true; }
  async send(client: ImageClient, png: Uint8Array, snapshotAt: string, signal: AbortSignal, progress: Progress): Promise<void> {
    this.cancelRequested = false;
    const operation = deadline(signal, IMAGE_TIMEOUT); let pending = false;
    if (await this.store.get()) throw new PendingError();
    // A frozen copy is both validated and transmitted; changing the preview cannot replace it.
    const image = png.slice(); await validatePNG(image); check(operation);
    const target = await client.target(operation); const id = this.random(16);
    if (id.length !== 16 || id.every(v => v === 0)) throw new Error('无法生成图片事务 ID');
    const record: Pending = { schema: 1, phase: 'pending', deviceID: client.info.id, bindingID: client.bindingID || undefined, requestID: hex(id), digest: hex(await sha256(image)), size: image.length, ...target, snapshotAt, createdAt: new Date().toISOString() };
    const cancelPoint = async () => {
      check(operation);
      if (!this.cancelRequested) return;
      const receipt = await exchange(client, target, wire(4, record), operation);
      if (!matches(record, receipt) || receipt.state !== 5 || receipt.code !== 0) throw new Error('上传已中断；未确认取消回复，未提交刷新');
      throw new Error('已取消上传，未提交刷新');
    };
    try {
      const begin = concat(u32(record.size), unhex(record.digest, 32));
      requireReceiving(record, await exchange(client, target, wire(1, record, begin), operation), 0);
      await cancelPoint();
      progress('uploading', 0, record.size);
      for (let offset = 0; offset < image.length;) {
        await cancelPoint(); const end = Math.min(offset + 106, image.length);
        const receipt = await exchange(client, target, wire(2, record, concat(u32(offset), image.subarray(offset, end))), operation);
        requireReceiving(record, receipt, end); offset = end; progress('uploading', offset, record.size);
      }
      await cancelPoint();
      // Durable identity MUST commit before any potentially admitted COMMIT I/O.
      await this.store.put(record); pending = true; check(operation);
      // Persistence is asynchronous. A stop requested while IndexedDB commits
      // must still prevent the physical COMMIT. Clear only after an exact,
      // authenticated CANCEL receipt and conditional durable removal succeed.
      if (this.cancelRequested) {
        const canceled = await exchange(client, target, wire(4, record), operation);
        if (!matches(record, canceled) || canceled.state !== 5 || canceled.code !== 0) throw new PendingError();
        await this.store.remove(record);
        pending = false;
        throw new Error('已取消上传，未提交刷新');
      }
      progress('displaying', record.size, record.size);
      const receipt = await exchange(client, target, wire(3, record), operation, true);
      await this.finishOrPoll(client, record, receipt, operation, progress);
    } catch (error) {
      if (error instanceof UnknownOutcomeError) throw error;
      if (pending) throw new PendingError();
      throw error;
    }
  }
  async recover(client: ImageClient, signal: AbortSignal, progress: Progress): Promise<void> {
    const record = await this.store.get(); if (!record) throw new Error('没有待确认的刷新');
    if (record.deviceID !== client.info.id) throw new Error(`请选择原设备 ${record.deviceID} 后查询`);
    if ((record.bindingID ?? '') !== (client.bindingID ?? '')) throw new UnknownOutcomeError();
    const operation = deadline(signal, IMAGE_TIMEOUT);
    let target: Target;
    try { target = await client.target(operation); } catch { throw new UnknownOutcomeError(); }
    if (target.generation !== record.generation || target.revision !== record.revision) throw new UnknownOutcomeError();
    try {
      const receipt = await exchange(client, record, wire(5, record), operation);
      await this.finishOrPoll(client, record, receipt, operation, progress);
    } catch (error) {
      if (error instanceof UnknownOutcomeError) throw error;
      throw new PendingError();
    }
  }
  private async finishOrPoll(client: ImageClient, record: Pending, first: Receipt, signal: AbortSignal, progress: Progress): Promise<void> {
    let receipt = first;
    for (;;) {
      if (!matches(record, receipt)) throw new UnknownOutcomeError();
      if (receipt.state === 3 && receipt.code === 0 && receipt.received === record.size) {
        await this.store.remove(record); progress('complete', record.size, record.size); return;
      }
      if (receipt.state === 4 || receipt.state === 5) {
        // A signed terminal error is known, but retain identity until deliberate user acknowledgement.
        throw new UnknownOutcomeError();
      }
      if (receipt.state !== 2 || receipt.code !== 0 || receipt.received !== record.size) throw new UnknownOutcomeError();
      progress('displaying', record.size, record.size);
      await sleep(2000, signal);
      // Do not repeat COMMIT, even if its original response was lost.
      receipt = await exchange(client, record, wire(5, record), signal);
    }
  }
}
