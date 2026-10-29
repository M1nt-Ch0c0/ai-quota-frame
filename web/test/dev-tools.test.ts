import test from 'node:test';
import assert from 'node:assert/strict';
import { OP_ABORT, OP_ACTIVATE, OP_BEGIN, OP_CHUNK, OP_COMMIT, OP_REMOVE, manage, parsePackage, slotRows, stagePackage } from '../src/dev-tools.ts';
import { DeviceError } from '../src/ble/session.ts';
import { concat, sha256, u32, utf8, view } from '../src/bytes.ts';
const signal=()=>AbortSignal.timeout(10000);
async function makePackage(version=21,app='photoframe',size=1500){
  const elf=new Uint8Array(size);elf.set([0x7f,0x45,0x4c,0x46,1,1,1]);view(elf).setUint16(16,3,true);view(elf).setUint16(18,94,true);
  const header=new Uint8Array(4096),v=view(header);v.setUint32(0,0x31464c45,true);v.setUint32(4,2,true);v.setUint32(8,2,true);v.setUint32(12,elf.length,true);v.setUint32(16,version,true);
  header.set(await sha256(elf),20);header.set(utf8(app),52);return concat(header,elf);
}
class FakeSession {
  ops:number[]=[];received=0;resume=0;failAt=-1;busy=0;
  async command(op:number,payload:Uint8Array){
    this.ops.push(op);
    if(op===OP_CHUNK){const offset=view(payload).getUint32(0,true);assert.equal(offset,this.received);assert.ok(payload.length<=475);
      if(this.failAt>=0&&this.ops.filter(o=>o===OP_CHUNK).length===this.failAt)throw new DeviceError(5);
      this.received+=payload.length-4;return u32(this.received);}
    return new Uint8Array();
  }
  async busyCommand(op:number,payload:Uint8Array){
    if(this.busy){this.busy--;this.ops.push(-op);}
    this.ops.push(op);
    if(op===OP_BEGIN){assert.equal(payload.length,68);this.received=this.resume;return u32(this.resume);}
    return new Uint8Array();
  }
}
test('package parser accepts a format-2 package and reports identity',async()=>{const pkg=await parsePackage(await makePackage(21,'photoframe'));assert.equal(pkg.app,'photoframe');assert.equal(pkg.version,21);assert.match(pkg.digest,/^[0-9a-f]{64}$/);});
test('package parser rejects corrupted payload, bad magic and bad app id',async()=>{
  const bad=await makePackage();bad[4096+100]^=1;await assert.rejects(parsePackage(bad),/校验失败/);
  const magic=await makePackage();magic[0]=0;await assert.rejects(parsePackage(magic),/不是有效/);
  const id=await makePackage(21,'Photo');await assert.rejects(parsePackage(id),/标识无效/);
});
test('staging uploads every chunk in order then commits exactly once',async()=>{const s=new FakeSession(),pkg=await parsePackage(await makePackage());const seen:number[]=[];
  await stagePackage(s,pkg,signal(),sent=>seen.push(sent));assert.equal(s.received,pkg.bytes.length);assert.equal(seen.at(-1),pkg.bytes.length);
  assert.equal(s.ops.filter(o=>o===OP_COMMIT).length,1);assert.ok(!s.ops.includes(OP_ACTIVATE));assert.ok(!s.ops.includes(OP_ABORT));});
test('staging resumes at the offset acknowledged by BEGIN',async()=>{const s=new FakeSession(),pkg=await parsePackage(await makePackage());s.resume=471*3;
  await stagePackage(s,pkg,signal(),()=>{});assert.equal(s.received,pkg.bytes.length);assert.equal(s.ops.filter(o=>o===OP_CHUNK).length,Math.ceil((pkg.bytes.length-471*3)/471));});
test('a rejected chunk aborts the upload and never commits',async()=>{const s=new FakeSession(),pkg=await parsePackage(await makePackage());s.failAt=2;
  await assert.rejects(stagePackage(s,pkg,signal(),()=>{}),DeviceError);assert.ok(!s.ops.includes(OP_COMMIT));assert.equal(s.ops.at(-1),OP_ABORT);});
test('cancellation before COMMIT sends no COMMIT',async()=>{const s=new FakeSession(),pkg=await parsePackage(await makePackage()),abort=new AbortController();
  await assert.rejects(stagePackage(s,pkg,abort.signal,()=>abort.abort()));assert.ok(!s.ops.includes(OP_COMMIT));});
test('management sends one command with the encoded application id',async()=>{const s=new FakeSession();await manage(s,OP_REMOVE,'color-test',signal());assert.deepEqual(s.ops,[OP_REMOVE]);
  assert.throws(()=>manage(s,3,'photoframe',signal()));assert.throws(()=>manage(s,OP_REMOVE,'Bad ID',signal()));});
test('slot rows mark current, fallback, pending and running slots',()=>{
  const rows=slotRows({loaded_app:0,loaded:1,apps:[{id:'photoframe',bank:0,active:1,previous:0,pending:-1,trial:0,versions:[21,20]},{id:'demo',bank:1,active:0,previous:-1,pending:1,trial:0,versions:[3,4]}]});
  assert.deepEqual(rows[0].slots.map(s=>[s.version,s.tags]),[[21,['可回退']],[20,['当前','运行中']]]);assert.ok(rows[0].running);
  assert.deepEqual(rows[1].slots.map(s=>s.tags),[['当前'],['待激活']]);assert.ok(!rows[1].running);
  assert.deepEqual(slotRows({}),[]);
});
