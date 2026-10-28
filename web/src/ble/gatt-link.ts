import { check, deadline, sleep, view } from '../bytes.ts';
export const HOST_SERVICE = '7b9d0001-6c82-4f31-9a48-6f5061696e74';
const characteristic = (index: number) => `7b9d${index.toString(16).padStart(4, '0')}-6c82-4f31-9a48-6f5061696e74`;
export interface Link {
  info(signal: AbortSignal): Promise<Uint8Array>;
  challenge(signal: AbortSignal): Promise<Uint8Array>;
  exchange(frame: Uint8Array, signal: AbortSignal): Promise<Uint8Array>;
  disconnect(): void;
}
export function fragments(frame: Uint8Array): Uint8Array[] {
  if (!frame.length || frame.length > 512) throw new Error('蓝牙请求长度超限');
  const result: Uint8Array[] = [];
  for (let offset = 0; offset < frame.length; offset += 16) {
    const part = new Uint8Array(4 + Math.min(16, frame.length - offset));
    view(part).setUint16(0, offset, true); view(part).setUint16(2, frame.length, true);
    part.set(frame.subarray(offset, offset + 16), 4); result.push(part);
  }
  return result;
}
export class GattLink implements Link {
  private running = false;
  readonly device: BluetoothDevice;
  private readonly chars: BluetoothRemoteGATTCharacteristic[];
  private constructor(device: BluetoothDevice, chars: BluetoothRemoteGATTCharacteristic[]) { this.device = device; this.chars = chars; }
  static async connect(device: BluetoothDevice, signal: AbortSignal): Promise<GattLink> {
    if (!device.gatt) throw new Error('该设备没有可用的 GATT 服务');
    const abort = () => device.gatt?.disconnect();
    signal.addEventListener('abort', abort, { once: true });
    try {
      check(signal);
      const server = await device.gatt.connect(); check(signal);
      const service = await server.getPrimaryService(HOST_SERVICE); check(signal);
      const chars = [];
      for (let index = 2; index <= 5; index++) { chars.push(await service.getCharacteristic(characteristic(index))); check(signal); }
      return new GattLink(device, chars);
    } catch (error) { abort(); throw error; }
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
      for (const fragment of fragments(frame)) { check(signal); await this.chars[2].writeValueWithResponse(fragment as BufferSource); check(signal); }
      for (;;) {
        const value = await this.read(3, signal);
        if (value.length === 1 && value[0] === 0) { await sleep(25, signal); continue; }
        if (!value.length || value.length > 512) throw new Error('相框返回了无效长度的回复');
        return value;
      }
    });
  }
  disconnect() { this.device.gatt?.disconnect(); }
}
