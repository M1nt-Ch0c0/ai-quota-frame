import test from 'node:test';
import assert from 'node:assert/strict';
import { CPM_AUTH_KEY, SOURCE_AUTH_KEY, decodeStoredAuth, loadSourceCredential, saveSourceCredential, clearSourceCredential } from '../src/source-auth.ts';
const context={origin:'https://manager.example',host:'manager.example',userAgent:'test-agent'};
const fixtureKey='fixture-management-key-only';
function store(){const map=new Map<string,string>();return {getItem:(k:string)=>map.get(k)??null,setItem:(k:string,v:string)=>{map.set(k,v);},removeItem:(k:string)=>{map.delete(k);}};}
function encoded(value:unknown,version:number){
  const salt=`cli-proxy-api-webui::secure-storage|${version===1?context.host+'|'+context.userAgent:'v2|'+context.host}`;
  const key=Buffer.from(salt),data=Buffer.from(JSON.stringify(value));
  return `enc::v${version}::`+Buffer.from(data.map((v,i)=>v^key[i%key.length])).toString('base64');
}
for(const version of [0,1,2])test(`reuse same-origin CPM persisted session format ${version}`,()=>{
  const s=store(),auth={state:{managementKey:fixtureKey,apiBase:context.origin,rememberPassword:true},version:0};
  s.setItem(CPM_AUTH_KEY,version?encoded(auth,version):JSON.stringify(auth));
  assert.deepEqual(loadSourceCredential(s,context),{key:fixtureKey,source:'cpm'});
  assert.equal(s.getItem(SOURCE_AUTH_KEY),null);
});
test('manual remembered credential survives reload and browser user-agent changes',()=>{
  const s=store();saveSourceCredential(s,context,fixtureKey);
  assert.ok(!s.getItem(SOURCE_AUTH_KEY)!.includes(fixtureKey));
  assert.deepEqual(loadSourceCredential(s,{...context,userAgent:'new-browser-version'}),{key:fixtureKey,source:'saved'});
});
test('CPM login takes priority; clearing plugin storage preserves host login',()=>{
  const s=store();saveSourceCredential(s,context,fixtureKey);
  const host=JSON.stringify({state:{apiBase:context.origin,managementKey:'fixture-host-key'}});s.setItem(CPM_AUTH_KEY,host);
  assert.equal(loadSourceCredential(s,context)?.source,'cpm');
  clearSourceCredential(s);assert.equal(s.getItem(CPM_AUTH_KEY),host);assert.equal(s.getItem(SOURCE_AUTH_KEY),null);
});
test('non-remembered CPM session does not fabricate a credential',()=>{
  const s=store();s.setItem(CPM_AUTH_KEY,JSON.stringify({state:{apiBase:context.origin,rememberPassword:false}}));
  assert.equal(loadSourceCredential(s,context),undefined);
});
test('credentials from another API origin are rejected',()=>{
  const s=store();s.setItem(CPM_AUTH_KEY,JSON.stringify({state:{apiBase:'https://other.example',managementKey:fixtureKey}}));
  assert.throws(()=>loadSourceCredential(s,context),/其他服务器/);
});
test('unreadable storage fails explicitly without echoing the stored value',()=>{
  assert.throws(()=>decodeStoredAuth('enc::v2::not-valid-base64',context),/无法读取/);
  const s=store();s.setItem(SOURCE_AUTH_KEY,JSON.stringify({origin:'https://other.example',key:fixtureKey}));
  assert.throws(()=>loadSourceCredential(s,context),/配置无效/);
});
test('storage write failures are surfaced, not reported as remembered',()=>{
  const s=store();s.setItem=()=>{throw new Error('storage blocked');};
  assert.throws(()=>saveSourceCredential(s,context,fixtureKey),/storage blocked/);
});
