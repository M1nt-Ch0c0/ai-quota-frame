import test from 'node:test';
import assert from 'node:assert/strict';
import { Sender, PendingError, UnknownOutcomeError } from '../src/photoframe/sender.ts';
import type { Pending, PendingStore } from '../src/photoframe/store.ts';
import { encodePNG, WIDTH, HEIGHT } from '../src/png.ts';
import { concat, u32, utf8, view } from '../src/bytes.ts';
const validPNG=await encodePNG(new Uint8Array(WIDTH*HEIGHT).fill(1));
const signal=()=>AbortSignal.timeout(10000),at=()=>new Date().toISOString();
class MemoryStore implements PendingStore {
  pending?:Pending; fail=false;
  async get(){return this.pending;}
  async put(record:Pending){if(this.fail||this.pending)throw new Error('storage denied');this.pending=structuredClone(record);}
  async remove(expected:Pending){assert.equal(this.pending?.requestID,expected.requestID);this.pending=undefined;}
}
class FakeClient {
  bindingID?: string;
  info={id:'000102030405'};generation=7;revision=9;commands:number[]=[];size=0;received=0;digest=new Uint8Array(32);id=new Uint8Array(16);last=0;state=1;dropCommit=false;changeDigest=false;missing=false;store?:MemoryStore;
  async target(){return {generation:this.generation,revision:this.revision};}
  async busyCommand(op:number,payload:Uint8Array){
    assert.equal(view(payload).getUint32(0,true),this.generation);assert.equal(view(payload).getUint32(4,true),this.revision);
    if(op===14){const cmd=payload.subarray(12);this.last=cmd[1];this.commands.push(this.last);this.id=cmd.slice(2,18);
      if(this.last===1){this.size=view(cmd).getUint32(18,true);this.digest=cmd.slice(22);this.received=0;this.state=1;}
      else if(this.last===2){assert.equal(view(cmd).getUint32(18,true),this.received);assert.ok(cmd.length<=128);this.received+=cmd.length-22;}
      else if(this.last===3){assert.ok(this.store?.pending,'COMMIT must follow durable store');this.state=3;if(this.dropCommit){this.dropCommit=false;throw new Error('COMMIT response lost');}}
      else if(this.last===4)this.state=5;
      else if(this.last===5&&this.missing)this.state=6;
      return new Uint8Array();
    }
    assert.equal(op,13);assert.equal(view(payload).getUint32(8,true),1);
    const digest=this.digest.slice();if(this.changeDigest)digest[0]^=1;
    return concat(utf8('PFI1'),new Uint8Array([this.state,this.last,0,0]),this.id,u32(this.size),u32(this.received),digest);
  }
}
function fixture(){const store=new MemoryStore(),client=new FakeClient();client.store=store;return {store,client,sender:new Sender(store,()=>new Uint8Array(16).fill(3))};}
test('multi-chunk upload requires exact signed receipt then clears its durable identity',async()=>{const {store,client,sender}=fixture();const progress:number[]=[];await sender.send(client,validPNG,at(),signal(),(_,done)=>progress.push(done));assert.equal(client.commands.filter(n=>n===3).length,1);assert.ok(client.commands.filter(n=>n===2).length>1);assert.equal(progress.at(-1),validPNG.length);assert.equal(store.pending,undefined);});
test('persistence failure never sends COMMIT',async()=>{const {store,client,sender}=fixture();store.fail=true;await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}));assert.ok(!client.commands.includes(3));});
test('lost COMMIT response survives new sender and recovers with QUERY only',async()=>{const {store,client,sender}=fixture();client.dropCommit=true;await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}),PendingError);const identity=store.pending!.requestID;const before=client.commands.length;await new Sender(store).recover(client,signal(),()=>{});assert.deepEqual(client.commands.slice(before),[5]);assert.equal(client.commands.filter(n=>n===3).length,1);assert.equal(identity,'03'.repeat(16));assert.equal(store.pending,undefined);});
test('generation or revision changes preserve pending and never resend',async()=>{for(const field of ['generation','revision'] as const){const {store,client,sender}=fixture();client.dropCommit=true;await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}));client[field]++;const count=client.commands.length;await assert.rejects(sender.recover(client,signal(),()=>{}),UnknownOutcomeError);assert.equal(client.commands.length,count);assert.ok(store.pending);}});
test('missing and mismatching receipts cannot report success',async()=>{for(const mode of ['missing','changeDigest'] as const){const {store,client,sender}=fixture();client.dropCommit=true;await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}));client[mode]=true;await assert.rejects(sender.recover(client,signal(),()=>{}),UnknownOutcomeError);assert.ok(store.pending);}});
test('pre-commit abort sends no COMMIT',async()=>{const {store,client,sender}=fixture();const abort=new AbortController();await assert.rejects(sender.send(client,validPNG,at(),abort.signal,()=>abort.abort()));assert.ok(!client.commands.includes(3));assert.equal(store.pending,undefined);});
test('explicit pre-commit cancel uses CANCEL and never COMMIT',async()=>{const {store,client,sender}=fixture();await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>sender.cancelUpload()),/已取消上传/);assert.ok(client.commands.includes(4));assert.ok(!client.commands.includes(3));assert.equal(store.pending,undefined);});
test('cancel during durable put sends CANCEL and removes only its pending identity, never COMMIT',async()=>{
  const {store,client,sender}=fixture(); const put=store.put.bind(store);
  store.put=async record=>{await put(record);sender.cancelUpload();};
  await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}),/已取消上传/);
  assert.ok(client.commands.includes(4));assert.ok(!client.commands.includes(3));assert.equal(store.pending,undefined);
});
test('uncertain cancellation after durable put retains recovery identity and never COMMIT',async()=>{
  const {store,client,sender}=fixture(); const put=store.put.bind(store); const command=client.busyCommand.bind(client);
  store.put=async record=>{await put(record);sender.cancelUpload();};
  client.busyCommand=async(op,payload)=>{if(op===14&&payload[13]===4)throw new Error('CANCEL reply lost');return command(op,payload);};
  await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}),PendingError);
  assert.ok(store.pending);assert.ok(!client.commands.includes(3));
});
test('a second device cannot overwrite an existing pending request',async()=>{const {store,client,sender}=fixture();client.dropCommit=true;await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}));const saved=structuredClone(store.pending);const second=new FakeClient();second.info.id='aabbccddeeff';second.store=store;await assert.rejects(new Sender(store).send(second,validPNG,at(),signal(),()=>{}),PendingError);assert.deepEqual(store.pending,saved);assert.equal(second.commands.length,0);});
test('physical re-pairing cannot query or redraw an earlier owner transaction',async()=>{
  const {store,client,sender}=fixture();client.bindingID='aa'.repeat(16);client.dropCommit=true;
  await assert.rejects(sender.send(client,validPNG,at(),signal(),()=>{}),PendingError);
  assert.equal(store.pending?.bindingID,client.bindingID);
  client.bindingID='bb'.repeat(16);const count=client.commands.length;
  await assert.rejects(sender.recover(client,signal(),()=>{}),UnknownOutcomeError);
  assert.equal(client.commands.length,count);assert.ok(store.pending);
});
