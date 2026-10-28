// The only long-lived device credential is negotiated with a physical button
// approval. IndexedDB stores a non-exportable WebCrypto key, never a token.
import { durableTransaction } from '../idb.ts';
export interface BondSummary {
  schema: 1;
  deviceID: string;
  browserID: string;
  clientID: string;
  bindingID: string;
  name: string;
  pairCode?: string;
  createdAt: string;
  pairedAt?: string;
  phase: 'pending' | 'paired';
}
export interface StoredBond extends BondSummary { key: CryptoKey; }
export interface BondStore {
  list(): Promise<BondSummary[]>;
  get(deviceID: string): Promise<StoredBond | undefined>;
  putCandidate(bond: StoredBond): Promise<void>;
  confirm(expected: StoredBond): Promise<StoredBond>;
  discardCandidate(expected: StoredBond): Promise<void>;
  forget(deviceID: string): Promise<void>;
}
const identity = (value: unknown, length: number) => typeof value === 'string' &&
  new RegExp(`^[0-9a-f]{${length}}$`).test(value) && !/^0+$/.test(value);
function same(a: StoredBond | undefined, b: StoredBond): boolean {
  return !!a && a.deviceID === b.deviceID && a.bindingID === b.bindingID && a.clientID === b.clientID;
}
export function validateBond(value: unknown): StoredBond {
  const bond = value as StoredBond;
  if (!bond || bond.schema !== 1 || !identity(bond.deviceID, 12) || !identity(bond.clientID, 32) ||
    !identity(bond.bindingID, 32) || typeof bond.browserID !== 'string' || !bond.browserID || bond.browserID.length > 256 ||
    typeof bond.name !== 'string' || bond.name.length > 80 || !Number.isFinite(Date.parse(bond.createdAt)) ||
    !['pending', 'paired'].includes(bond.phase) || (bond.phase === 'paired' && !Number.isFinite(Date.parse(bond.pairedAt ?? ''))) ||
    (bond.phase === 'pending' && !/^\d{6}$/.test(bond.pairCode ?? ''))) {
    throw new Error('本机配对记录损坏，请在设备页重新配对');
  }
  const key = bond.key;
  if (!(key instanceof CryptoKey) || key.type !== 'secret' || key.extractable || key.algorithm.name !== 'HMAC' ||
    (key.algorithm as HmacKeyAlgorithm).length !== 256 || (key.algorithm as HmacKeyAlgorithm).hash.name !== 'SHA-256' ||
    !key.usages.includes('sign')) throw new Error('本机配对密钥不可用，请重新配对');
  return bond;
}
export function summarizeBond(bond: StoredBond): BondSummary {
  // A strict projection keeps credential handles out of UI state and logs.
  return { schema: 1, deviceID: bond.deviceID, browserID: bond.browserID, clientID: bond.clientID,
    bindingID: bond.bindingID, name: bond.name, pairCode: bond.pairCode, createdAt: bond.createdAt, pairedAt: bond.pairedAt, phase: bond.phase };
}
export class IndexedBondStore implements BondStore {
  private db?: Promise<IDBDatabase>;
  private open(): Promise<IDBDatabase> {
    return this.db ??= new Promise((resolve, reject) => {
      const request = indexedDB.open('photopainter-bindings-v2', 1);
      request.onupgradeneeded = () => {
        request.result.createObjectStore('bonds', { keyPath: 'deviceID' });
        request.result.createObjectStore('candidates', { keyPath: 'deviceID' });
      };
      request.onerror = () => reject(new Error('无法保存配对关系，请允许此站点使用本地存储'));
      request.onblocked = () => reject(new Error('配对记录正在被另一个页面使用，请关闭旧页面后重试'));
      request.onsuccess = () => resolve(request.result);
    });
  }
  private async readAll(name: 'bonds' | 'candidates'): Promise<StoredBond[]> {
    const db = await this.open();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(name, 'readonly'), request = tx.objectStore(name).getAll();
      tx.oncomplete = () => { try { resolve((request.result as unknown[]).map(validateBond)); } catch (error) { reject(error); } };
      tx.onerror = tx.onabort = () => reject(new Error('无法读取本机配对关系'));
    });
  }
  async list(): Promise<BondSummary[]> {
    const records = new Map<string, StoredBond>();
    for (const bond of await this.readAll('bonds')) records.set(bond.deviceID, bond);
    for (const bond of await this.readAll('candidates')) records.set(bond.deviceID, bond);
    return [...records.values()].map(summarizeBond).sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  }
  async get(deviceID: string): Promise<StoredBond | undefined> {
    if (!identity(deviceID, 12)) throw new Error('相框身份格式错误');
    const db = await this.open();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(['bonds', 'candidates'], 'readonly');
      const candidate = tx.objectStore('candidates').get(deviceID), active = tx.objectStore('bonds').get(deviceID);
      tx.oncomplete = () => {
        try { const value: unknown = candidate.result ?? active.result; resolve(value === undefined ? undefined : validateBond(value)); }
        catch (error) { reject(error); }
      };
      tx.onerror = tx.onabort = () => reject(new Error('无法读取本机配对关系'));
    });
  }
  async putCandidate(bond: StoredBond): Promise<void> {
    validateBond(bond); if (bond.phase !== 'pending') throw new Error('配对候选状态错误');
    const db = await this.open();
    await new Promise<void>((resolve, reject) => {
      const tx = durableTransaction(db, 'candidates');
      tx.objectStore('candidates').add(bond);
      tx.oncomplete = () => resolve();
      tx.onerror = tx.onabort = () => reject(new Error('配对关系尚未保存；请先处理已有配对请求'));
    });
  }
  async confirm(expected: StoredBond): Promise<StoredBond> {
    validateBond(expected);
    const db = await this.open();
    const paired: StoredBond = { ...expected, phase: 'paired', pairCode: undefined, pairedAt: new Date().toISOString() };
    await new Promise<void>((resolve, reject) => {
      const tx = durableTransaction(db, ['bonds', 'candidates']);
      const candidates = tx.objectStore('candidates'), request = candidates.get(expected.deviceID);
      request.onsuccess = () => {
        if (!same(request.result as StoredBond | undefined, expected)) { tx.abort(); return; }
        tx.objectStore('bonds').put(paired); candidates.delete(expected.deviceID);
      };
      tx.oncomplete = () => resolve();
      tx.onerror = tx.onabort = () => reject(new Error('设备可能已同意配对，本机确认未保存；重新连接即可恢复'));
    });
    return paired;
  }
  async discardCandidate(expected: StoredBond): Promise<void> {
    const db = await this.open();
    await new Promise<void>((resolve, reject) => {
      const tx = durableTransaction(db, 'candidates'), candidates = tx.objectStore('candidates');
      const request = candidates.get(expected.deviceID);
      request.onsuccess = () => {
        if (!same(request.result as StoredBond | undefined, expected)) { tx.abort(); return; }
        candidates.delete(expected.deviceID);
      };
      tx.oncomplete = () => resolve();
      tx.onerror = tx.onabort = () => reject(new Error('配对记录已变化，未清除其他关系'));
    });
  }
  async forget(deviceID: string): Promise<void> {
    if (!identity(deviceID, 12)) throw new Error('相框身份格式错误');
    const db = await this.open();
    await new Promise<void>((resolve, reject) => {
      const tx = durableTransaction(db, ['bonds', 'candidates']);
      tx.objectStore('bonds').delete(deviceID); tx.objectStore('candidates').delete(deviceID);
      tx.oncomplete = () => resolve();
      tx.onerror = tx.onabort = () => reject(new Error('无法移除本机配对记录'));
    });
  }
}
