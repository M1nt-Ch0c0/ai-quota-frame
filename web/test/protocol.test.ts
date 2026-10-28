import test from 'node:test';
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { Session, hmac, targetFromStatus, DeviceError } from '../src/ble/session.ts';
import { GattLink, fragments } from '../src/ble/gatt-link.ts';
import { concat, hex, u32, utf8, view } from '../src/bytes.ts';
const token='0123456789abcdef0123456789abcdef';
const signal=()=>AbortSignal.timeout(5000);
const tag=(key:Uint8Array,domain:string,data:Uint8Array)=>new Uint8Array(createHmac('sha256',key).update(domain).update(data).digest());
const info=new Uint8Array([1,2,0,1,2,3,4,5,192,168,1,42,0,0,0,0,0,0,0,0]);
class FakeLink {
  key=new Uint8Array(); requests:Uint8Array[]=[]; disconnected=false; mutate=''; busy=false;
  statusValue=utf8(JSON.stringify({padding:'x'.repeat(900),generation:7,ble_service_revision:9,ble_characteristics:2,ble_service_uuid:'a4fe200015364c88a2ce32ecdaac0471',runtime_ready:true,loaded_app:0,apps:[{id:'photoframe',bank:0}]}));
  async info(){return info.slice();}
  async challenge(){return Uint8Array.from({length:32},(_,i)=>i);}
  disconnect(){this.disconnected=true;}
  async exchange(p:Uint8Array){
    this.requests.push(p.slice());let payload=new Uint8Array(),seq=p[0]===1?0:view(p).getUint32(1,true),code=0;
    if(p[0]===1){const transcript=concat(info.subarray(2,8),await this.challenge(),p.subarray(1,33));assert.deepEqual(p.subarray(33),tag(utf8(token),'pp-ble-v1/client',transcript));this.key=tag(utf8(token),'pp-ble-v1/session',transcript);payload=info;}
    else{
      assert.deepEqual(p.subarray(-32),tag(this.key,'pp-ble-v1/request',p.subarray(0,-32)));
      if(this.busy){code=3;this.busy=false;}
      else if(p[0]===3){const offset=view(p).getUint32(5,true);payload=concat(u32(this.statusValue.length),u32(offset),this.statusValue.subarray(offset,offset+400));}
    }
    const header=concat(new Uint8Array([p[0]]),u32(seq),new Uint8Array([code,0]),payload);
    const out=concat(header,tag(this.key,'pp-ble-v1/response',header));
    if(this.mutate==='signature')out[out.length-1]^=1;
    if(this.mutate==='sequence')out[1]^=1;
    if(this.mutate==='unsigned')return out.subarray(0,7);
    return out;
  }
}
const connect=(link:FakeLink)=>Session.authenticate(link,token,'000102030405',signal(),Uint8Array.from({length:32},(_,i)=>i+32));
test('fixed C and Go HMAC vectors match WebCrypto',async()=>{
  const transcript=concat(info.subarray(2,8),Uint8Array.from({length:32},(_,i)=>i),Uint8Array.from({length:32},(_,i)=>i+32));
  assert.equal(hex(await hmac(utf8(token),'pp-ble-v1/client',transcript)),'827d2691b1e2ac5990b86832bbaae8eaa53767b4a807d027d5a13c62cd9b495c');
  assert.equal(hex(await hmac(utf8(token),'pp-ble-v1/session',transcript)),'2b1310093d3f4c44d104014ce803b48f19cfefc38a66f583daf21a5a3bd6aa2e');
  const link=new FakeLink(),client=await connect(link);assert.equal(client.info.id,'000102030405');assert.ok(!Buffer.from(link.requests[0]).includes(Buffer.from(token)));
});
test('signed status pages and frozen application target',async()=>{const link=new FakeLink(),client=await connect(link);assert.deepEqual(await client.target(signal()),{generation:7,revision:9});assert.equal(link.requests.filter(p=>p[0]===3).length,3);assert.throws(()=>targetFromStatus({runtime_ready:false}));});
test('signed BUSY retries with new sequence only',async()=>{const link=new FakeLink(),client=await connect(link);link.busy=true;await client.busyCommand(14,new Uint8Array(),signal());assert.deepEqual(link.requests.slice(1).map(p=>view(p).getUint32(1,true)),[1,2]);});
test('tamper, replay and unsigned responses close the session',async()=>{for(const mode of ['signature','sequence','unsigned']){const link=new FakeLink(),client=await connect(link);link.mutate=mode;await assert.rejects(client.command(3,u32(0),signal()));assert.ok(link.disconnected);const n=link.requests.length;await assert.rejects(client.command(3,u32(0),signal()));assert.equal(link.requests.length,n);}});
test('all logical frames fragment into <=20 byte writes',()=>{const frame=Uint8Array.from({length:512},(_,i)=>i),parts=fragments(frame);assert.equal(parts.length,32);assert.ok(parts.every(p=>p.length<=20));assert.deepEqual(concat(...parts.map(p=>p.subarray(4))),frame);assert.equal(view(parts[31]).getUint16(0,true),496);assert.throws(()=>fragments(new Uint8Array(513)));});
test('GATT operations serialize and pending long reads are not success',async()=>{
  let writes=0,reads=0,active=0,maxActive=0;
  const value=Uint8Array.from({length:500},(_,i)=>i);
  const tick=async<T>(result:T)=>{active++;maxActive=Math.max(maxActive,active);await new Promise(r=>setTimeout(r,1));active--;return result;};
  const chars=Array.from({length:4},(_,i)=>({readValue:async()=>{if(i===3){reads++;const bytes=reads===1?new Uint8Array([0]):value;return tick(new DataView(bytes.buffer));}return new DataView(info.buffer);},writeValueWithResponse:async()=>{writes++;await tick(undefined);}}));
  const device={id:'browser-id',gatt:{connected:true,connect:async()=>({getPrimaryService:async()=>({getCharacteristic:async(id:string)=>chars[parseInt(id.slice(4,8),16)-2]})}),disconnect(){this.connected=false;}}};
  const link=await GattLink.connect(device as never,signal());
  const operation=link.exchange(new Uint8Array(65),signal());await assert.rejects(link.exchange(new Uint8Array(1),signal()),/正在执行/);
  assert.deepEqual(await operation,value);assert.equal(writes,5);assert.equal(reads,2);assert.equal(maxActive,1);
});
