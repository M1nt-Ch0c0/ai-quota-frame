import test from 'node:test';
import assert from 'node:assert/strict';
import { ScreenAwake } from '../src/screen-awake.ts';
class Lock extends EventTarget {
  released = false;
  async release() { this.released = true; this.dispatchEvent(new Event('release')); }
}
const doc = { visibilityState: 'visible' };
Object.defineProperty(globalThis, 'document', { value: doc, configurable: true });
function api(request?: () => Promise<Lock>) {
  Object.defineProperty(globalThis, 'navigator', { value: { wakeLock: request ? { request } : undefined }, configurable: true });
}
test('screen awake follows visibility and explicit pause', async () => {
  const locks: Lock[] = []; api(async () => { const lock = new Lock(); locks.push(lock); return lock; });
  const awake = new ScreenAwake(() => {});
  await awake.set(true); assert.ok(awake.active); assert.equal(locks.length, 1);
  await awake.set(true); assert.equal(locks.length, 1);
  doc.visibilityState = 'hidden'; await awake.set(true); assert.equal(awake.active, false); assert.ok(locks[0].released);
  doc.visibilityState = 'visible'; await awake.set(true); assert.ok(awake.active); assert.equal(locks.length, 2);
  await awake.set(false); assert.ok(locks[1].released); assert.equal(awake.active, false);
});
test('a pending request cannot retain a lock after the schedule is paused', async () => {
  let resolve: (lock: Lock) => void = () => {}; api(() => new Promise(r => { resolve = r; }));
  const awake = new ScreenAwake(() => {}), pending = awake.set(true);
  await awake.set(false); const lock = new Lock(); resolve(lock); await pending;
  assert.ok(lock.released); assert.equal(awake.active, false);
});
test('denied, unsupported and revoked screen locks are surfaced', async () => {
  api(async () => { throw new DOMException('Permission denied', 'NotAllowedError'); });
  const awake = new ScreenAwake(() => {}); await awake.set(true);
  assert.equal(awake.active, false); assert.match(awake.error, /Permission denied/);
  api(); await awake.set(true); assert.match(awake.error, /不支持/);
  const lock = new Lock(); api(async () => lock); await awake.set(true); assert.ok(awake.active);
  await lock.release(); assert.equal(awake.active, false); assert.match(awake.error, /系统已释放/);
});
