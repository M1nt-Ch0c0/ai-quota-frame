import test from 'node:test';
import assert from 'node:assert/strict';
import 'fake-indexeddb/auto';
import { IndexedBondStore, summarizeBond, type StoredBond } from '../src/ble/bonds.ts';
import { durableTransaction } from '../src/idb.ts';
async function candidate(deviceID: string, bindingID: string): Promise<StoredBond> {
  return { schema: 1, deviceID, browserID: `browser-${deviceID}`, name: 'Test frame', clientID: '11'.repeat(16), bindingID,
    createdAt: new Date().toISOString(), phase: 'pending', pairCode: '123456',
    key: await crypto.subtle.importKey('raw', new Uint8Array(32).fill(83), {name: 'HMAC', hash: 'SHA-256'}, false, ['sign']) };
}
test('pairing key remains non-exportable across IndexedDB and lost confirmation reload', async () => {
  const store = new IndexedBondStore(), bond = await candidate('aabbccddeeff', '22'.repeat(16));
  await store.putCandidate(bond);
  const recovered = await new IndexedBondStore().get(bond.deviceID); assert.ok(recovered);
  assert.equal(recovered.phase, 'pending');
  await assert.rejects(crypto.subtle.exportKey('raw', recovered.key));
  assert.equal('key' in summarizeBond(recovered), false);
  assert.ok(!JSON.stringify(await store.list()).includes('secret'));
  await store.confirm(recovered);
  const confirmed = await store.get(bond.deviceID); assert.equal(confirmed?.phase, 'paired');
  assert.ok(confirmed?.pairedAt); await store.forget(bond.deviceID);
  assert.equal(await store.get(bond.deviceID), undefined);
});
test('candidate creation and confirmation cannot overwrite another concurrent relation', async () => {
  const a = new IndexedBondStore(), b = new IndexedBondStore();
  const one = await candidate('001122334455', '33'.repeat(16)), two = await candidate('001122334455', '44'.repeat(16));
  const writes = await Promise.allSettled([a.putCandidate(one), b.putCandidate(two)]);
  assert.equal(writes.filter(r => r.status === 'fulfilled').length, 1);
  const saved = await a.get(one.deviceID); assert.ok(saved);
  const wrong = saved.bindingID === one.bindingID ? two : one;
  await assert.rejects(b.confirm(wrong));
  await assert.rejects(b.discardCandidate(wrong));
  assert.equal((await a.get(one.deviceID))?.bindingID, saved.bindingID);
  await a.discardCandidate(saved); assert.equal(await b.get(one.deviceID), undefined);
});
test('unsupported strict durability fails closed before any device transaction write', () => {
  let aborted=false;
  const ignored={transaction:()=>({durability:'default',abort(){aborted=true;}})} as unknown as IDBDatabase;
  assert.throws(()=>durableTransaction(ignored,'bonds'),/更新 Chrome/);assert.equal(aborted,true);
  let calls=0;
  const old={transaction:()=>{calls++;throw new TypeError('options unavailable');}} as unknown as IDBDatabase;
  assert.throws(()=>durableTransaction(old,'bonds'),/更新 Chrome/);assert.equal(calls,1,'must not fall back to relaxed writes');
});
