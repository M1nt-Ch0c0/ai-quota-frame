// Developer application maintenance over the existing authenticated session.
// Each mutation is sent at most once; a lost reply is resolved by reading a
// fresh signed status after reconnecting, never by repeating the command.
import { concat, equal, hex, sha256, u32, utf8, view } from './bytes.ts';

export const OP_BEGIN = 4, OP_CHUNK = 5, OP_COMMIT = 6, OP_ABORT = 7;
export const OP_ACTIVATE = 9, OP_SWITCH = 10, OP_ROLLBACK = 11, OP_REMOVE = 12;
export const MAX_PACKAGE = 1048576;
const HEADER = 4096, CHUNK = 471;

export interface AppPackage { bytes: Uint8Array; app: string; version: number; abi: number; digest: string; }
export interface SlotRow { id: string; bank: number; slots: { version?: number; tags: string[] }[]; trial: boolean; running: boolean; }
export interface MaintenanceSession {
  command(op: number, payload: Uint8Array, signal: AbortSignal, long?: boolean): Promise<Uint8Array>;
  busyCommand(op: number, payload: Uint8Array, signal: AbortSignal, long?: boolean): Promise<Uint8Array>;
}

export function encodeApp(id: string): Uint8Array {
  if (!/^[a-z0-9_-]{1,31}$/.test(id)) throw new Error('应用标识格式错误');
  const result = new Uint8Array(32); result.set(utf8(id)); return result;
}
export async function parsePackage(bytes: Uint8Array): Promise<AppPackage> {
  const v = view(bytes);
  if (bytes.length < HEADER + 52 || bytes.length > MAX_PACKAGE || v.getUint32(0, true) !== 0x31464c45 ||
      v.getUint32(12, true) !== bytes.length - HEADER) throw new Error('不是有效的应用安装包（大小或标识不符）');
  const format = v.getUint32(4, true), abi = v.getUint32(8, true);
  if (!(format === 1 && abi === 1 || format === 2 && (abi === 1 || abi === 2))) throw new Error('安装包格式或 ABI 不受支持');
  let app = 'photoframe';
  if (format === 2) {
    const raw = bytes.subarray(52, 84), end = raw.indexOf(0);
    app = new TextDecoder().decode(end < 0 ? raw : raw.subarray(0, end));
    if (!/^[a-z0-9_-]{1,31}$/.test(app) || !equal(encodeApp(app), raw)) throw new Error('安装包应用标识无效');
  }
  if (!equal(await sha256(bytes.subarray(HEADER)), bytes.subarray(20, 52))) throw new Error('安装包内容校验失败');
  const elf = bytes.subarray(HEADER), ev = view(elf);
  if (!equal(elf.subarray(0, 7), new Uint8Array([0x7f, 0x45, 0x4c, 0x46, 1, 1, 1])) || ev.getUint16(16, true) !== 3 || ev.getUint16(18, true) !== 94) {
    throw new Error('安装包内不是相框应用程序');
  }
  return { bytes, app, version: v.getUint32(16, true), abi, digest: hex(await sha256(bytes)) };
}

// Stage writes the slot opposite the active one; activation is separate.
export async function stagePackage(session: MaintenanceSession, pkg: AppPackage, signal: AbortSignal,
  progress: (sent: number, total: number) => void): Promise<void> {
  const total = pkg.bytes.length;
  const ack = await session.busyCommand(OP_BEGIN, concat(u32(total), await sha256(pkg.bytes), encodeApp(pkg.app)), signal);
  if (ack.length !== 4 || view(ack).getUint32(0, true) > total) throw new Error('相框返回的上传位置无效');
  let committed = false;
  try {
    for (let offset = view(ack).getUint32(0, true); offset < total;) {
      const end = Math.min(offset + CHUNK, total);
      const reply = await session.command(OP_CHUNK, concat(u32(offset), pkg.bytes.subarray(offset, end)), signal);
      if (reply.length !== 4 || view(reply).getUint32(0, true) !== end) throw new Error('相框确认的上传位置不一致');
      offset = end; progress(offset, total);
    }
    signal.throwIfAborted(); committed = true;
    await session.command(OP_COMMIT, new Uint8Array(), signal, true);
  } catch (error) {
    // Only an upload that never reached COMMIT is released; COMMIT itself may have run.
    if (!committed) await session.command(OP_ABORT, new Uint8Array(), AbortSignal.timeout(10000)).catch(() => {});
    throw error;
  }
}
export function manage(session: MaintenanceSession, op: number, app: string, signal: AbortSignal): Promise<Uint8Array> {
  if (![OP_ACTIVATE, OP_SWITCH, OP_ROLLBACK, OP_REMOVE].includes(op)) throw new Error('未知的管理操作');
  return session.busyCommand(op, encodeApp(app), signal, true);
}

export function slotRows(status: Record<string, unknown>): SlotRow[] {
  if (!Array.isArray(status.apps)) return [];
  const int = (v: unknown) => typeof v === 'number' && Number.isInteger(v) ? v : -1;
  return status.apps.filter(a => a && typeof a.id === 'string').map(a => {
    const versions: unknown[] = Array.isArray(a.versions) ? a.versions : [];
    const running = int(status.loaded_app) === int(a.bank);
    return {
      id: a.id, bank: int(a.bank), trial: a.trial === 1 || a.trial === true, running,
      slots: [0, 1].map(slot => ({
        version: typeof versions[slot] === 'number' ? versions[slot] as number : undefined,
        tags: [slot === int(a.active) ? '当前' : '', slot === int(a.previous) ? '可回退' : '', slot === int(a.pending) ? '待激活' : '',
          running && slot === int(status.loaded) ? '运行中' : ''].filter(Boolean),
      })),
    };
  });
}
