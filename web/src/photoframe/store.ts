import { unhex } from '../bytes.ts';
import type { Target } from '../ble/session.ts';
import { durableTransaction } from '../idb.ts';
export interface Pending extends Target {
  schema: 1;
  deviceID: string;
  bindingID?: string;
  requestID: string;
  digest: string;
  size: number;
  snapshotAt: string;
  createdAt: string;
  phase: 'pending';
}
export interface PendingStore {
  get(): Promise<Pending | undefined>;
  put(record: Pending): Promise<void>;
  remove(expected: Pending): Promise<void>;
}
export function validatePending(value: unknown): Pending {
  const record = value as Pending;
  if (!record || record.schema !== 1 || record.phase !== 'pending' || !/^[0-9a-f]{12}$/.test(record.deviceID) ||
    !Number.isInteger(record.size) || record.size < 1 || record.size > 5 * 1024 * 1024 ||
    !Number.isInteger(record.generation) || record.generation < 1 || record.generation > 0xffffffff ||
    !Number.isInteger(record.revision) || record.revision < 1 || record.revision > 0xffffffff ||
    !Number.isFinite(Date.parse(record.snapshotAt)) || !Number.isFinite(Date.parse(record.createdAt))) throw new Error('待确认记录损坏，请人工核对设备画面后清除');
  const id = unhex(record.requestID, 16); unhex(record.digest, 32);
  if (record.bindingID !== undefined && (!/^[0-9a-f]{32}$/.test(record.bindingID) || /^0+$/.test(record.bindingID))) throw new Error('待确认记录的配对关系无效');
  if (id.every(v => v === 0)) throw new Error('待确认请求 ID 无效');
  return record;
}
export class IndexedPendingStore implements PendingStore {
  private db?: Promise<IDBDatabase>;
  private open(): Promise<IDBDatabase> {
    return this.db ??= new Promise((resolve, reject) => {
      const request = indexedDB.open('photopainter-web-v1', 1);
      request.onupgradeneeded = () => request.result.createObjectStore('transactions');
      request.onerror = () => reject(new Error('无法打开待确认记录；请允许此站点使用本地存储'));
      request.onblocked = () => reject(new Error('待确认存储被旧页面占用，请关闭其他标签页'));
      request.onsuccess = () => resolve(request.result);
    });
  }
  private async run<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
    const db = await this.open();
    return new Promise((resolve, reject) => {
      const transaction = mode === 'readwrite' ? durableTransaction(db, 'transactions') : db.transaction('transactions', mode); const request = action(transaction.objectStore('transactions'));
      transaction.oncomplete = () => resolve(request.result);
      transaction.onerror = transaction.onabort = () => reject(new Error('无法保存待确认记录；本次不会提交刷新'));
    });
  }
  async get(): Promise<Pending | undefined> { const value: unknown = await this.run('readonly', store => store.get('pending')); return value === undefined ? undefined : validatePending(value); }
  async put(record: Pending): Promise<void> { validatePending(record); await this.run('readwrite', store => store.add(record, 'pending')); }
  async remove(expected: Pending): Promise<void> {
    const db = await this.open();
    await new Promise<void>((resolve, reject) => {
      const tx = durableTransaction(db, 'transactions'); const store = tx.objectStore('transactions');
      const read = store.get('pending');
      read.onsuccess = () => {
        const current = read.result as Pending | undefined;
        if (!current || current.deviceID !== expected.deviceID || current.bindingID !== expected.bindingID || current.requestID !== expected.requestID || current.digest !== expected.digest) { tx.abort(); return; }
        store.delete('pending');
      };
      tx.oncomplete = () => resolve();
      tx.onerror = tx.onabort = () => reject(new Error('待确认记录已变化，未清除其他事务'));
    });
  }
}
