import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createECDH, createHash, createHmac, hkdfSync } from 'node:crypto';
import { clientCommit, serverCommit, pairingTranscript, deriveRoot, comparisonCode, pairDevice, connectPaired, PairingError } from '../src/ble/pairing.ts';
import { hmac } from '../src/ble/session.ts';
import type { BondStore, StoredBond } from '../src/ble/bonds.ts';
import { concat, hex, utf8, view } from '../src/bytes.ts';
const vector=JSON.parse(readFileSync(new URL('./fixtures/pairing-v2.json',import.meta.url),'utf8')) as Record<string,string>;
test('Go and browser share the exact independent pairing vectors',()=>{
  assert.deepEqual(readFileSync(new URL('./fixtures/pairing-v2.json',import.meta.url)),readFileSync(new URL('../../internal/ble/testdata/pairing-v2.json',import.meta.url)));
});
const bytes=(name:string)=>new Uint8Array(Buffer.from(vector[name],'hex'));
const cat=(...parts:(Uint8Array|string)[])=>Buffer.concat(parts.map(p=>typeof p==='string'?Buffer.from(p):Buffer.from(p)));
const sha=(...parts:(Uint8Array|string)[])=>createHash('sha256').update(cat(...parts)).digest();
const mac=(key:Uint8Array,domain:string,p:Uint8Array)=>createHmac('sha256',key).update(domain).update(p).digest();
const v2=new Uint8Array([2]);
const signal=()=>AbortSignal.timeout(8000);
const options={deviceID:vector.deviceID,browserID:'test-browser-device',name:'PhotoPainter test',clientName:vector.name};
async function fixedPrivate():Promise<CryptoKey>{
  const pub=bytes('clientPublic');
  return crypto.subtle.importKey('jwk',{kty:'EC',crv:'P-256',x:Buffer.from(pub.subarray(1,33)).toString('base64url'),y:Buffer.from(pub.subarray(33)).toString('base64url'),d:Buffer.from(bytes('clientPrivate')).toString('base64url'),ext:false,key_ops:['deriveBits']},{name:'ECDH',namedCurve:'P-256'},false,['deriveBits']);
}
test('OpenSSL golden commitments, transcript, HKDF, SAS and proofs match WebCrypto',async()=>{
  const n=utf8(vector.name),d=bytes('deviceID'),cid=bytes('clientID'),bid=bytes('bindingID');
  const cc=await clientCommit(d,cid,bid,bytes('clientPublic'),bytes('clientNonce'),n);assert.equal(hex(cc),vector.clientCommit);
  const sc=await serverCommit(d,cid,bid,cc,bytes('serverPublic'),bytes('serverNonce'),n);assert.equal(hex(sc),vector.serverCommit);
  const t=pairingTranscript(d,cid,bid,cc,sc,bytes('clientPublic'),bytes('clientNonce'),bytes('serverPublic'),bytes('serverNonce'),n);assert.equal(hex(t),vector.transcript);
  const root=await deriveRoot(await fixedPrivate(),bytes('serverPublic'),t);await assert.rejects(crypto.subtle.exportKey('raw',root));
  assert.equal(await comparisonCode(root,t),vector.code);
  assert.equal(hex(await hmac(root,'pp-pair-v2/poll',bytes('pollTranscript'))),vector.pollProof);
  assert.equal(hex(await hmac(root,'pp-pair-v2/cancel',bytes('pollTranscript'))),vector.cancelProof);
  assert.equal(hex(await hmac(root,'pp-bond-v2/client',bytes('authTranscript'))),vector.authProof);
  assert.equal(hex(await hmac(root,'pp-bond-v2/session',bytes('authTranscript'))),vector.session);
});
class MemoryBonds implements BondStore {
  candidate?:StoredBond; active?:StoredBond; fail=false;
  async list(){return this.candidate?[this.candidate]:this.active?[this.active]:[];}
  async get(){return this.candidate??this.active;}
  async putCandidate(b:StoredBond){if(this.fail||this.candidate)throw new Error('store unavailable');this.candidate=b;}
  async confirm(b:StoredBond){assert.equal(this.candidate?.bindingID,b.bindingID);this.active={...b,phase:'paired',pairedAt:new Date().toISOString(),pairCode:undefined};this.candidate=undefined;return this.active;}
  async discardCandidate(b:StoredBond){assert.equal(this.candidate?.bindingID,b.bindingID);this.candidate=undefined;}
  async forget(){this.candidate=undefined;this.active=undefined;}
}
function reply(op:number,payload:Uint8Array,root?:Uint8Array,domain='pp-pair-v2/response',code=0):Uint8Array {
  const h=new Uint8Array(7);h[0]=op;view(h).setUint16(5,code,true);const body=concat(h,payload);return root?concat(body,mac(root,domain,body)):body;
}
class FakeHost {
  infoBytes=concat(new Uint8Array([2,0]),bytes('deviceID'),new Uint8Array(12));challengeBytes=bytes('challenge');
  state=0;cid=new Uint8Array(16);bid=this.challengeBytes.slice(0,16);name=new Uint8Array();cc=new Uint8Array();sc=new Uint8Array();spub=new Uint8Array();root=new Uint8Array();
  private=createECDH('prime256v1');commands:number[]=[];approve=false;owner=false;dropApproval=false;tamper=false;closed=false;approvals=0;store:MemoryBonds;
  unsignedBoundCancel=false;approveOnCancel=false;
  constructor(store:MemoryBonds){this.store=store;this.private.setPrivateKey(bytes('serverPrivate'));this.spub=new Uint8Array(this.private.getPublicKey(null,'uncompressed'));}
  async info(){return this.infoBytes.slice();}async challenge(){return this.challengeBytes.slice();}disconnect(){this.closed=true;}
  status(op:number,code=0){const payload=concat(v2,this.bid,new Uint8Array([this.state]),new Uint8Array(4),this.challengeBytes);view(payload).setUint32(18,120000,true);return reply(op,payload,this.root,undefined,code);}
  async exchange(p:Uint8Array):Promise<Uint8Array>{
    this.commands.push(p[0]);
    if(p[0]===18){return reply(18,concat(new Uint8Array([2,this.state]),this.bid,this.cid,new Uint8Array(4),new Uint8Array([this.name.length]),this.name));}
    if(p[0]===19){
      assert.equal(this.owner,false);assert.equal(p.length,66+p[65]);this.cid=p.slice(1,17);this.bid=p.slice(17,33);this.cc=p.slice(33,65);this.name=p.slice(66);
      assert.deepEqual(this.bid,this.challengeBytes.subarray(0,16));
      this.sc=new Uint8Array(sha('pp-pair-v2/server-commit',v2,bytes('deviceID'),this.cid,this.bid,this.cc,this.spub,bytes('serverNonce'),new Uint8Array([this.name.length]),this.name));this.state=1;
      const sc=this.sc.slice();if(this.tamper)sc[0]^=1;return reply(19,concat(v2,this.bid,this.spub,sc));
    }
    if(p[0]===20){
      assert.equal(p.length,114);const cpub=p.slice(17,82),cnonce=p.slice(82);
      assert.deepEqual(this.cc,new Uint8Array(sha('pp-pair-v2/client-commit',v2,bytes('deviceID'),this.cid,this.bid,cpub,cnonce,new Uint8Array([this.name.length]),this.name)));
      const t=cat(v2,bytes('deviceID'),this.cid,this.bid,this.cc,this.sc,cpub,cnonce,this.spub,bytes('serverNonce'),new Uint8Array([this.name.length]),this.name);
      this.root=new Uint8Array(hkdfSync('sha256',this.private.computeSecret(cpub),sha('pp-pair-v2/salt',t),Buffer.from('pp-pair-v2/bond'),32));this.state=2;
      const payload=concat(v2,this.bid,bytes('serverNonce'),new Uint8Array([2]),new Uint8Array(4),this.challengeBytes);view(payload).setUint32(50,30000,true);return reply(20,payload,this.root);
    }
    if(p[0]===21||p[0]===22){
      assert.equal(p.length,49);assert.deepEqual(p.subarray(1,17),this.bid);
      const domain=p[0]===21?'pp-pair-v2/poll':'pp-pair-v2/cancel';assert.deepEqual(p.subarray(17),new Uint8Array(mac(this.root,domain,cat(v2,bytes('deviceID'),this.cid,this.bid,this.challengeBytes))));
      assert.ok(this.store.candidate,'device display may only arm after candidate is durable');
      if(p[0]===22){
        if(this.approveOnCancel&&!this.owner){this.owner=true;this.state=5;this.approvals++;}
        if(this.unsignedBoundCancel)return reply(22,new Uint8Array(),undefined,undefined,8);
        if(this.owner)return this.status(22,8);this.state=6;return this.status(22);
      }
      if(this.approve&&!this.owner){this.owner=true;this.approvals++;this.state=5;if(this.dropApproval){this.dropApproval=false;throw new Error('approval response lost');}}
      if(!this.owner)this.state=4;
      return this.status(21);
    }
    if(p[0]===23){
      if(!this.owner)return reply(23,new Uint8Array(),undefined,undefined,1);
      assert.equal(p.length,97);assert.deepEqual(p.subarray(1,17),this.cid);assert.deepEqual(p.subarray(17,33),this.bid);
      const a=cat(v2,bytes('deviceID'),this.cid,this.bid,this.challengeBytes,p.subarray(33,65));assert.deepEqual(p.subarray(65),new Uint8Array(mac(this.root,'pp-bond-v2/client',a)));
      return reply(23,this.infoBytes,mac(this.root,'pp-bond-v2/session',a),'pp-ble-v1/response');
    }
    throw new Error(`unexpected op ${p[0]}`);
  }
}
test('physical approval lost response resumes candidate without repeating pair ceremony',async()=>{
  const store=new MemoryBonds(),host=new FakeHost(store);host.dropApproval=true;
  await assert.rejects(pairDevice(host,options,store,signal(),p=>{if(p.stage==='confirm-on-frame')host.approve=true;}),e=>e instanceof PairingError&&e.kind==='pending');
  assert.ok(store.candidate);assert.equal(store.active,undefined);assert.equal(host.approvals,1);
  const bid=store.candidate.bindingID;host.challengeBytes=new Uint8Array(32).fill(0x92);host.closed=false;
  const client=await pairDevice(host,options,store,signal(),()=>{});
  assert.equal(client.bindingID,bid);assert.equal(store.active?.phase,'paired');assert.equal(store.candidate,undefined);
  assert.equal(host.commands.filter(op=>op===19).length,1);assert.equal(host.commands.filter(op=>op===20).length,1);assert.equal(host.approvals,1);
  const active=store.active!;host.owner=false;host.state=0;host.challengeBytes=new Uint8Array(32).fill(0x93);
  await assert.rejects(connectPaired(host,active,store,signal()));assert.equal(host.commands.filter(op=>op===19).length,1);
});
test('server commitment tamper and storage failure never arm the physical prompt',async()=>{
  for(const mode of ['commitment','storage']){const store=new MemoryBonds(),host=new FakeHost(store);if(mode==='commitment')host.tamper=true;else store.fail=true;
    await assert.rejects(pairDevice(host,options,store,signal(),()=>{}));assert.equal(host.commands.includes(21),false);assert.equal(host.owner,false);assert.equal(store.active,undefined);}
});
test('user cancellation serializes signed cancel after storage, without arming or binding',async()=>{
  const store=new MemoryBonds(),host=new FakeHost(store),cancel=new AbortController();
  await assert.rejects(pairDevice(host,options,store,signal(),p=>{if(p.stage==='saving')cancel.abort();},cancel.signal),e=>e instanceof PairingError&&e.kind==='cancelled');
  assert.equal(host.commands.includes(21),false);assert.equal(host.commands.filter(op=>op===22).length,1);assert.equal(store.candidate,undefined);assert.equal(host.owner,false);
});
test('physical approval winning CANCEL is proven with BondAuth even when STATE is unsigned',async()=>{
  const store=new MemoryBonds(),host=new FakeHost(store),cancel=new AbortController();host.unsignedBoundCancel=true;
  const client=await pairDevice(host,options,store,signal(),p=>{if(p.stage==='confirm-on-frame'){host.approveOnCancel=true;cancel.abort();}},cancel.signal);
  assert.equal(store.active?.phase,'paired');assert.equal(store.candidate,undefined);assert.equal(client.bindingID,store.active?.bindingID);
  assert.equal(host.approvals,1);assert.deepEqual(host.commands,[18,19,20,21,22,23]);
});
test('unsigned CANCEL STATE without an approved owner cannot create a binding',async()=>{
  const store=new MemoryBonds(),host=new FakeHost(store),cancel=new AbortController();host.unsignedBoundCancel=true;
  await assert.rejects(pairDevice(host,options,store,signal(),p=>{if(p.stage==='saving')cancel.abort();},cancel.signal),e=>e instanceof PairingError&&e.kind==='pending');
  assert.equal(store.active,undefined);assert.ok(store.candidate);assert.equal(host.approvals,0);
  assert.deepEqual(host.commands,[18,19,20,22,23]);
});
