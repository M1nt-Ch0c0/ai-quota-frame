// Device approval and COMMIT must not follow a merely relaxed disk write.
// Require strict durability instead of silently weakening the pairing promise.
export function durableTransaction(db: IDBDatabase, stores: string | string[]): IDBTransaction {
  let tx: IDBTransaction;
  try { tx = db.transaction(stores, 'readwrite', { durability: 'strict' }); }
  catch (error) {
    if (!(error instanceof TypeError)) throw error;
    throw new Error('当前浏览器无法可靠保存设备事务，请更新 Chrome 后重试');
  }
  if (tx.durability !== 'strict') {
    try { tx.abort(); } catch { /* No writes have been issued. */ }
    throw new Error('当前浏览器无法可靠保存设备事务，请更新 Chrome 后重试');
  }
  return tx;
}
