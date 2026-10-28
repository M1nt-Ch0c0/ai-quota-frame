#!/usr/bin/env python3
"""Exercise the exported C ABI without a server or real credentials."""
import base64
import ctypes as c
import hashlib
import gzip
import json
import pathlib
import sys

class Buffer(c.Structure):
    _fields_=[('ptr',c.c_void_p),('len',c.c_size_t)]
class Host(c.Structure):
    _fields_=[('abi_version',c.c_uint32),('host_ctx',c.c_void_p),('call',c.c_void_p),('free_buffer',c.c_void_p)]
class Plugin(c.Structure):
    _fields_=[('abi_version',c.c_uint32),('call',c.c_void_p),('free_buffer',c.c_void_p),('shutdown',c.c_void_p)]

library=c.CDLL(sys.argv[1])
library.cliproxy_plugin_init.argtypes=[c.POINTER(Host),c.POINTER(Plugin)]
plugin=Plugin();host=Host(1,None,None,None)
assert library.cliproxy_plugin_init(c.byref(host),c.byref(plugin))==0
assert plugin.abi_version==1 and plugin.call and plugin.free_buffer and plugin.shutdown
invoke=c.CFUNCTYPE(c.c_int,c.c_char_p,c.c_void_p,c.c_size_t,c.POINTER(Buffer))(plugin.call)
free=c.CFUNCTYPE(None,c.c_void_p,c.c_size_t)(plugin.free_buffer)
def call(method,request):
    payload=json.dumps(request).encode();result=Buffer()
    assert invoke(method.encode(),c.cast(c.c_char_p(payload),c.c_void_p),len(payload),c.byref(result))==0
    try:return json.loads(c.string_at(result.ptr,result.len))
    finally:free(result.ptr,result.len)
registration=call('plugin.register',{})
assert registration['ok'] and registration['result']['schema_version']==7
assert registration['result']['capabilities']=={'management_api':True}
resources=call('management.register',{})['result']['resources']
assert {r['Path'] for r in resources}=={'/panel','/frame.wasm','/wasm_exec.js'}
assert sum(bool(r.get('Menu')) for r in resources)==1
response=call('management.handle',{'Method':'GET','Path':'/v0/resource/plugins/photopainter/panel'})['result']
assert response['StatusCode']==200
body=base64.b64decode(response['Body']);expected=pathlib.Path(sys.argv[2]).read_bytes()
assert body==expected,'embedded HTML differs from final web bundle'
asset_results={}
for name in ['frame.wasm','wasm_exec.js']:
    response=call('management.handle',{'Method':'GET','Path':'/v0/resource/plugins/photopainter/'+name})['result']
    assert response['StatusCode']==200
    asset=base64.b64decode(response['Body'])
    disk=pathlib.Path(sys.argv[2]).parent/(name+'.gz' if name=='frame.wasm' else name)
    assert asset==disk.read_bytes(),'embedded shared engine differs from final web bundle'
    if name=='frame.wasm':
        assert response['Headers']['Content-Encoding']==['gzip']
        assert gzip.decompress(asset).startswith(b'\x00asm')
    asset_results[name]={'bytes':len(asset),'sha256':hashlib.sha256(asset).hexdigest()}
assert call('management.handle',{'Method':'GET','Path':'/v0/management/config'})['result']['StatusCode']==404
assert not call('executor.execute',{})['ok']
print(json.dumps({'abi':plugin.abi_version,'schema':7,'library_bytes':pathlib.Path(sys.argv[1]).stat().st_size,'library_sha256':hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest(),'version':registration['result']['metadata']['Version'],'resource':'/v0/resource/plugins/photopainter/panel','html_bytes':len(body),'html_sha256':hashlib.sha256(body).hexdigest(),'assets':asset_results,'capabilities':['management_api']},ensure_ascii=False))
