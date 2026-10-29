// Developer maintenance invoked explicitly through the connected phone's CDP.
// Bond CryptoKeys remain inside this browser origin; only metadata is returned.
import { IndexedBondStore } from '../src/ble/bonds.ts';
import { GattLink, HOST_SERVICE } from '../src/ble/gatt-link.ts';
import { Session } from '../src/ble/session.ts';
import { IndexedPendingStore } from '../src/photoframe/store.ts';
import { lockDevice } from '../src/lock.ts';
import { concat, equal, hex, sha256, u32, utf8, view } from '../src/bytes.ts';
export async function installApp(encoded: string, expectedID: string, expectedDigest: string, expectedVersion: number,
  report: (value: Record<string,unknown>) => void = () => {}): Promise<Record<string, unknown>> {
  const operation = AbortSignal.timeout(300000);
  const pkg = Uint8Array.from(atob(encoded), c => c.charCodeAt(0));
  if (hex(await sha256(pkg)) !== expectedDigest || pkg.length < 4148 || pkg.length > 1048576 ||
      view(pkg).getUint32(0,true) !== 0x31464c45 || view(pkg).getUint32(4,true) !== 2 ||
      view(pkg).getUint32(8,true) !== 2 || view(pkg).getUint32(12,true) !== pkg.length - 4096 ||
      view(pkg).getUint32(16,true) !== expectedVersion || !equal(await sha256(pkg.subarray(4096)),pkg.subarray(20,52))) {
    throw new Error('Package identity, format, length or digest mismatch; nothing installed');
  }
  if (await new IndexedPendingStore().get()) throw new Error('An image result is pending; maintenance refused');
  const bond = await new IndexedBondStore().get(expectedID);
  if (!bond || bond.phase !== 'paired') throw new Error('No confirmed phone pairing for expected device');
  const release = await lockDevice(bond.browserID);
  let session: Session | undefined, link: GattLink | undefined;
  try {
    let device: BluetoothDevice | undefined;
    if (typeof navigator.bluetooth.getDevices === 'function') device = (await navigator.bluetooth.getDevices()).find(d => d.id === bond.browserID);
    if (!device) device = await navigator.bluetooth.requestDevice({filters:[{services:[HOST_SERVICE]}]});
    if (device.id !== bond.browserID) throw new Error('Selected device does not match the existing phone pairing');
    link = await GattLink.connect(device, operation);
    session = await Session.authenticateBond(link,bond,operation);
    const before = await session.status(operation);
    report({stage:'connected',device_id:expectedID,version:before.version,uptime_ms:before.uptime_ms});
    const app = new Uint8Array(32);app.set(utf8('photoframe'));
    const begin = await session.busyCommand(4,concat(u32(pkg.length),await sha256(pkg),app),operation);
    if (begin.length !== 4 || view(begin).getUint32(0,true) !== 0) throw new Error('Unexpected application staging offset');
    for (let offset = 0; offset < pkg.length;) {
      const end = Math.min(offset+471,pkg.length);
      const reply = await session.command(5,concat(u32(offset),pkg.subarray(offset,end)),operation);
      if (reply.length !== 4 || view(reply).getUint32(0,true) !== end) throw new Error('Application staging offset not confirmed');
      offset = end;
      if (offset === pkg.length || offset % (471*16) === 0) report({stage:'staging',received:offset,total:pkg.length});
    }
    // COMMIT stages an inactive application slot. ACTIVATE is explicit and is
    // issued once, only after the signed stage completion has been verified.
    await session.command(6,new Uint8Array(),operation,true);
    report({stage:'staged',version:expectedVersion});
    await session.busyCommand(9,app,operation,true);
    const after = await session.status(operation);
    if (after.version !== expectedVersion || after.payload_abi_minor !== 3 || after.runtime_ready !== true || after.confirmed !== true) {
      throw new Error('Updated application has not completed a confirmed ABI 2.3 startup');
    }
    return {complete:true,device_id:expectedID,version:after.version,payload_abi_minor:after.payload_abi_minor,
      ready:after.runtime_ready,confirmed:after.confirmed,runtime_diagnostic:after.runtime_diagnostic,
      uptime_ms:after.uptime_ms,reset_reason:after.reset_reason,package_sha256:expectedDigest};
  } finally { if (session) session.disconnect(); else link?.disconnect();release(); }
}
