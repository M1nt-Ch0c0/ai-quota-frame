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
  maxRequestBytes=4096;channelMode=false;key=new Uint8Array(); requests:Uint8Array[]=[]; disconnected=false; mutate=''; busy=false;
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
      else if(this.channelMode&&p[0]===24)payload=concat(u32(7),u32(11),u32(4047),u32(64),Uint8Array.from(Buffer.from('a4fe200015364c88a2ce32ecdaac0471','hex')));
      else if(this.channelMode&&p[0]===25){assert.deepEqual(p.subarray(5,-32),concat(u32(7),u32(11)));payload=u32(99);}
      else if(this.channelMode&&p[0]===26){assert.equal(p.length,4096);assert.deepEqual(p.subarray(5,17),concat(u32(7),u32(11),u32(99)));payload=new Uint8Array(64).fill(7);}
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
  const chars=Array.from({length:5},(_,i)=>({readValue:async()=>{if(i===4)return new DataView(new Uint8Array([1,244,0]).buffer);if(i===3){reads++;const bytes=reads===1?new Uint8Array([0]):value;return tick(new DataView(bytes.buffer));}return new DataView(info.buffer);},writeValueWithResponse:async()=>{writes++;await tick(undefined);}}));
  const device={id:'browser-id',gatt:{connected:true,connect:async()=>({getPrimaryService:async()=>({getCharacteristic:async(id:string)=>chars[parseInt(id.slice(4,8),16)-2]})}),disconnect(){this.connected=false;}}};
  const link=await GattLink.connect(device as never,signal());
  const operation=link.exchange(new Uint8Array(65),signal());await assert.rejects(link.exchange(new Uint8Array(1),signal()),/正在执行/);
  assert.deepEqual(await operation,value);assert.equal(writes,1);assert.equal(reads,2);assert.equal(maxActive,1);
});

test('negotiated capacity increases fragments without losing bytes',()=>{
  const frame=Uint8Array.from({length:512},(_,i)=>i);
  for(const capacity of [20,182,244,512]) {
    const parts=fragments(frame,capacity);
    assert.equal(parts.length,Math.ceil(frame.length/(capacity-4)));
    assert.ok(parts.every(p=>p.length<=capacity));
    assert.deepEqual(concat(...parts.map(p=>p.subarray(4))),frame);
  }
  for(const capacity of [19,513,NaN,20.5]) assert.throws(()=>fragments(frame,capacity));
});

test('connection cancellation preserves the timeout cause instead of the disconnect symptom',async()=>{
  const controller=new AbortController();
  const timeout=new DOMException('连接和服务发现超时，请重试','TimeoutError');
  let disconnected=false;
  const device={gatt:{connect:async()=>({getPrimaryService:async()=>{
    controller.abort(timeout);
    throw new DOMException('GATT Server is disconnected','NetworkError');
  }}),disconnect(){disconnected=true;}}};
  await assert.rejects(GattLink.connect(device as never,controller.signal),error=>error===timeout);
  assert.ok(disconnected);
});

test('service discovery reconnects once after a network disconnect without sending commands',async()=>{
  let connects=0,disconnects=0,writes=0;
  const device={gatt:{connect:async()=>{
    connects++;
    return {getPrimaryService:async()=>{
      if(connects===1)throw new DOMException('GATT Server is disconnected','NetworkError');
      return {getCharacteristic:async(id:string)=>{if(id.includes('0007-'))throw new DOMException('Characteristic unavailable','NotFoundError');return {writeValueWithResponse:async()=>{writes++;}};}};
    }};
  },disconnect(){disconnects++;}}};
  await GattLink.connect(device as never,signal());
  assert.equal(connects,2);assert.equal(disconnects,1);assert.equal(writes,0);
});

test('readiness failure distinguishes startup failure from missing image service',()=>{
  assert.throws(()=>targetFromStatus({runtime_ready:false,last_result:-1,phase:'fault'}),/结果码 -1，阶段 fault/);
  assert.throws(()=>targetFromStatus({runtime_ready:true,loaded_app:0,apps:[{id:'photoframe',bank:0}],generation:3}),/已运行，但图片接收服务/);
});

test('GATT capacity is reused after negotiation, but the initial minimum is refreshed',async()=>{
  let probes=0,writes=0;
  const response=new Uint8Array(39);
  const chars=Array.from({length:5},(_,i)=>({
    readValue:async()=>new DataView(i===4?new Uint8Array([1,++probes===1?20:244,0]).buffer:response.buffer),
    writeValueWithResponse:async()=>{writes++;}
  }));
  const device={gatt:{connected:true,connect:async()=>({getPrimaryService:async()=>({getCharacteristic:async(id:string)=>chars[parseInt(id.slice(4,8),16)-2]})}),disconnect(){this.connected=false;}}};
  const link=await GattLink.connect(device as never,signal());
  for(let i=0;i<3;i++)await link.exchange(new Uint8Array(128),signal());
  assert.equal(probes,2);assert.equal(writes,10);
});

test('4-KiB generic messages use ordered bulk writes and one final reception fence',async()=>{
  const commands:Uint8Array[]=[],requests:Uint8Array[]=[];
  let reads=0;
  const chars=Array.from({length:6},(_,i)=>({
    readValue:async()=>{
      if(i===4)return new DataView(new Uint8Array([1,244,0]).buffer);
      if(i===5)return new DataView(new Uint8Array([1,0,16,0,2,1]).buffer);
      reads++;return new DataView(new Uint8Array(103).buffer);
    },
    writeValueWithoutResponse:async(p:Uint8Array)=>{commands.push(p.slice());},
    writeValueWithResponse:async(p:Uint8Array)=>{requests.push(p.slice());}
  }));
  const device={gatt:{connected:true,connect:async()=>({getPrimaryService:async()=>({getCharacteristic:async(id:string)=>chars[parseInt(id.slice(4,8),16)-2]})}),disconnect(){this.connected=false;}}};
  const link=await GattLink.connect(device as never,signal());assert.equal(link.maxRequestBytes,4096);
  const frame=Uint8Array.from({length:4096},(_,i)=>i);frame[0]=26;
  await link.exchange(frame,signal());
  assert.equal(commands.length,17);assert.equal(requests.length,1);assert.equal(reads,1);
  assert.deepEqual(concat(...[...commands,...requests].map(p=>p.subarray(4))),frame);
  await assert.rejects(link.exchange(new Uint8Array(4096),signal()),/长度超限/);
});

test('authenticated generic channel negotiates service, generation and exact message budget',async()=>{
  const link=new FakeLink();link.channelMode=true;
  const state=JSON.parse(new TextDecoder().decode(link.statusValue));state.payload_abi_minor=3;link.statusValue=utf8(JSON.stringify(state));
  const client=await connect(link);await client.target(signal());
  const service=Uint8Array.from(Buffer.from('a4fe200015364c88a2ce32ecdaac0471','hex'));
  await assert.rejects(client.openChannel(service,8,signal()),/代次/);
  const channel=await client.openChannel(service,7,signal());assert.ok(channel);assert.equal(channel.maxMessageBytes,4047);
  assert.deepEqual(await channel.exchange(new Uint8Array(4047),signal()),new Uint8Array(64).fill(7));
  assert.throws(()=>channel.exchange(new Uint8Array(4048),signal()),/容量/);
  assert.throws(()=>channel.exchange(new Uint8Array(),signal()),/容量/);
  assert.equal(link.requests.filter(p=>p[0]===26).length,1);
});

test('immediate reconnect waits for native teardown and remains cancellable',async()=>{
  let connects=0;
  const device={gatt:{connected:true,connect:async()=>{connects++;device.gatt.connected=true;return {getPrimaryService:async()=>({getCharacteristic:async(id:string)=>{if(id.includes('0007-'))throw new DOMException('Unavailable','NotFoundError');return {};}})};},disconnect(){this.connected=false;}}};
  const first=await GattLink.connect(device as never,signal());first.disconnect();
  await assert.rejects(GattLink.connect(device as never,AbortSignal.timeout(10)),e=>(e as DOMException).name==='TimeoutError');
  assert.equal(connects,1);
  const start=performance.now();await GattLink.connect(device as never,signal());
  assert.ok(performance.now()-start>=900);assert.equal(connects,2);
});
