import test from 'node:test';
import assert from 'node:assert/strict';
import 'fake-indexeddb/auto';
import { IndexedPendingStore, type Pending } from '../src/photoframe/store.ts';
import { lockDevice } from '../src/lock.ts';
const record=(id:string):Pending=>({schema:1,phase:'pending',deviceID:'aabbccddeeff',requestID:id.repeat(32),digest:'ab'.repeat(32),size:100,generation:7,revision:9,snapshotAt:new Date().toISOString(),createdAt:new Date().toISOString()});
test('IndexedDB atomically refuses pending overwrite and only deletes matching identity',async()=>{
  const a=new IndexedPendingStore(),b=new IndexedPendingStore(),one=record('1'),two=record('2');
  const results=await Promise.allSettled([a.put(one),b.put(two)]);assert.equal(results.filter(r=>r.status==='fulfilled').length,1);
  const saved=await a.get();assert.ok(saved);const wrong=saved.requestID===one.requestID?two:one;
  await assert.rejects(b.remove(wrong));assert.deepEqual(await a.get(),saved);await a.remove(saved);assert.equal(await b.get(),undefined);
});
test('same-origin lock covers different devices before GATT starts',async()=>{
  let held=false;
  const locks = {request:async(_name:string,_options:unknown,callback:(lock:unknown)=>Promise<void>)=>{if(held)return callback(null);held=true;try{return await callback({name:'lock'});}finally{held=false;}}};
  Object.defineProperty(globalThis,'navigator',{configurable:true,value:{locks}});
  const release=await lockDevice('one');await assert.rejects(lockDevice('two'),/另一标签页/);release();await new Promise(r=>setTimeout(r,0));const second=await lockDevice('two');second();
});
test('missing Web Locks fails before a connection can be opened',async()=>{
  Object.defineProperty(globalThis,'navigator',{configurable:true,value:{}});
  await assert.rejects(lockDevice('one'),/独占相框连接/);
});
