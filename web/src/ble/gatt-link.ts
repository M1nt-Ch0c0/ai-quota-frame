import { check, deadline, sleep, view } from '../bytes.ts';
export const GATT_CONNECT_TIMEOUT = 120000;
export const HOST_SERVICE = '7b9d0001-6c82-4f31-9a48-6f5061696e74';
// Android can still be tearing down the native GATT client after the JS
// disconnect returns. Serialize a short teardown window before reusing it.
const reconnectAfter = new WeakMap<BluetoothDevice, number>();
const characteristic = (index: number) => `7b9d${index.toString(16).padStart(4, '0')}-6c82-4f31-9a48-6f5061696e74`;
export interface Link {
  readonly maxRequestBytes?: number;
  info(signal: AbortSignal): Promise<Uint8Array>;
  challenge(signal: AbortSignal): Promise<Uint8Array>;
  exchange(frame: Uint8Array, signal: AbortSignal): Promise<Uint8Array>;
  disconnect(): void;
}
export function fragments(frame: Uint8Array, writeCapacity = 20, maxFrame = 512): Uint8Array[] {
  if (!frame.length || frame.length > maxFrame || ![512,4096].includes(maxFrame)) throw new Error('蓝牙请求长度超限');
  if (!Number.isInteger(writeCapacity) || writeCapacity < 20 || writeCapacity > 512) throw new Error('蓝牙写入容量无效');
  const payloadSize = writeCapacity - 4;
  const result: Uint8Array[] = [];
  for (let offset = 0; offset < frame.length; offset += payloadSize) {
    const part = new Uint8Array(4 + Math.min(payloadSize, frame.length - offset));
    view(part).setUint16(0, offset, true); view(part).setUint16(2, frame.length, true);
    part.set(frame.subarray(offset, offset + payloadSize), 4); result.push(part);
  }
  return result;
}
export class GattLink implements Link {
  private running = false;
  private writeCapacity: number | undefined;
  readonly maxRequestBytes: number;
  private readonly fastWrites: boolean;
  readonly device: BluetoothDevice;
  private readonly chars: BluetoothRemoteGATTCharacteristic[];
  private constructor(device: BluetoothDevice, chars: BluetoothRemoteGATTCharacteristic[], maxRequestBytes: number, fastWrites: boolean) { this.device = device; this.chars = chars; this.maxRequestBytes = maxRequestBytes; this.fastWrites = fastWrites; }
  static async connect(device: BluetoothDevice, signal: AbortSignal): Promise<GattLink> {
    if (!device.gatt) throw new Error('该设备没有可用的 GATT 服务');
    const abort = () => device.gatt?.disconnect();
    signal.addEventListener('abort', abort, { once: true });
    try {
      check(signal);
      const teardown = (reconnectAfter.get(device) ?? 0) - performance.now();
      if (teardown > 0) await sleep(teardown, signal);
      for (let attempt = 0; ; attempt++) {
        try {
          const server = await device.gatt.connect(); check(signal);
          const service = await server.getPrimaryService(HOST_SERVICE); check(signal);
          const chars = [];
          for (let index = 2; index <= 6; index++) { chars.push(await service.getCharacteristic(characteristic(index))); check(signal); }
          let maxRequestBytes = 512, fastWrites = false;
          let extended: BluetoothRemoteGATTCharacteristic | undefined;
          try { extended = await service.getCharacteristic(characteristic(7)); }
          catch (error) { if (!(error instanceof DOMException) || error.name !== 'NotFoundError') throw error; }
          if (extended) {
            const limits = await extended.readValue(); check(signal);
            if (limits.byteLength !== 6 || limits.getUint8(0) !== 1 || limits.getUint16(1,true) !== 4096 || limits.getUint16(3,true) !== 512 || limits.getUint8(5) !== 1) throw new Error('相框通用消息能力不兼容');
            maxRequestBytes = 4096; fastWrites = true;
          }
          return new GattLink(device, chars, maxRequestBytes, fastWrites);
        } catch (error) {
          check(signal);
          // Windows can lose the first connection while rediscovering a changed
          // GATT database. Only retry discovery; no protocol command was sent.
          if (attempt !== 0 || !(error instanceof DOMException) || error.name !== 'NetworkError') throw error;
          device.gatt.disconnect();
          await sleep(500, signal);
        }
      }
    } catch (error) { abort(); signal.throwIfAborted(); throw error; }
    finally { signal.removeEventListener('abort', abort); }
  }
  private async guarded<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
    check(signal);
    if (this.running) throw new Error('已有蓝牙请求正在执行');
    if (!this.device.gatt?.connected) throw new Error('相框已断开，请重新连接');
    this.running = true;
    let rejectAbort: (reason: unknown) => void = () => {};
    const stopped = new Promise<never>((_, reject) => { rejectAbort = reject; });
    const abort = () => { this.disconnect(); rejectAbort(signal.reason); };
    signal.addEventListener('abort', abort, { once: true });
    try { check(signal); return await Promise.race([work(), stopped]); }
    finally { signal.removeEventListener('abort', abort); this.running = false; }
  }
  private async read(index: number, signal: AbortSignal): Promise<Uint8Array> {
    const data = await this.chars[index].readValue(); check(signal);
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength).slice();
  }
  info(signal: AbortSignal) { const limited = deadline(signal, 20000); return this.guarded(limited, () => this.read(0, limited)); }
  challenge(signal: AbortSignal) { const limited = deadline(signal, 20000); return this.guarded(limited, () => this.read(1, limited)); }
  exchange(frame: Uint8Array, signal: AbortSignal) {
    return this.guarded(signal, async () => {
      let capacity = this.writeCapacity;
      if (capacity === undefined) {
        const limits = await this.read(4, signal);
        if (limits.length !== 3 || limits[0] !== 1) throw new Error('相框传输能力格式无效');
        capacity = view(limits).getUint16(1, true);
      }
      const channel = frame[0] === 26;
      const pieces = fragments(frame, capacity, channel ? this.maxRequestBytes : 512);
      // The initial minimum may precede MTU exchange. Cache the negotiated
      // larger capacity for this connection; a new link probes again.
      if (capacity > 20) this.writeCapacity = capacity;
      for (let i = 0; i < pieces.length; i++) {
        check(signal);
        // Ordered Write Commands carry the bulk body. The final Write Request
        // fences reception before awaiting the authenticated application reply.
        if (channel && this.fastWrites && i + 1 < pieces.length) await this.chars[2].writeValueWithoutResponse(pieces[i] as BufferSource);
        else await this.chars[2].writeValueWithResponse(pieces[i] as BufferSource);
        check(signal);
      }
      for (;;) {
        const value = await this.read(3, signal);
        if (value.length === 1 && value[0] === 0) { await sleep(25, signal); continue; }
        if (!value.length || value.length > 512) throw new Error('相框返回了无效长度的回复');
        return value;
      }
    });
  }
  disconnect() { reconnectAfter.set(this.device, performance.now() + 1000); this.device.gatt?.disconnect(); }
}
