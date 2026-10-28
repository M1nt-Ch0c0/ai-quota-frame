// PhotoPainter serves the shared browser frame engine and pairing interface.
// Its sole authenticated data projection exposes an xAI account subject, never
// the credential document that the native collector can read locally.
package main

/*
#include "native_host.h"
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unsafe"
)

//go:embed panel.html
var panel []byte

//go:embed frame.wasm.gz
var frameWASM []byte

//go:embed wasm_exec.js
var wasmRuntime []byte

const version = "0.2.2"

type envelope struct {
	OK     bool `json:"ok"`
	Result any  `json:"result,omitempty"`
	Error  any  `json:"error,omitempty"`
}

type managementResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil || host == nil || host.abi_version != 1 {
		return 1
	}
	plugin.abi_version = 1
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	C.photopainter_set_host(host)
	callHost = nativeHostCall
	return 0
}

func nativeHostCall(method string, request []byte) ([]byte, error) {
	if (method != "host.auth.list" && method != "host.auth.get") || len(request) > 8*1024*1024 {
		return nil, errors.New("unsupported account metadata request")
	}
	name := C.CString(method)
	defer C.free(unsafe.Pointer(name))
	var data unsafe.Pointer
	if len(request) > 0 {
		data = C.CBytes(request)
		defer C.free(data)
	}
	var response C.cliproxy_buffer
	code := C.photopainter_host_call(name, (*C.uint8_t)(data), C.size_t(len(request)), &response)
	defer C.photopainter_host_free(response.ptr, response.len)
	if code != 0 || response.ptr == nil || response.len == 0 || uint64(response.len) > 8*1024*1024 {
		return nil, errors.New("account metadata unavailable")
	}
	return C.GoBytes(response.ptr, C.int(response.len)), nil
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, size C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return 1
	}
	response.ptr, response.len = nil, 0
	if method == nil || uint64(size) > 8*1024*1024 {
		return 1
	}
	var raw []byte
	if request != nil && size > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(size))
	}
	data, err := json.Marshal(handleMethod(C.GoString(method), raw))
	if err != nil {
		return 1
	}
	response.ptr, response.len = C.CBytes(data), C.size_t(len(data))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(pointer unsafe.Pointer, size C.size_t) {
	if pointer != nil {
		C.free(pointer)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, raw []byte) envelope {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return envelope{OK: true, Result: map[string]any{
			"schema_version": 7,
			"metadata":       map[string]any{"Name": "photopainter", "Version": version, "Author": "M1nt-Ch0c0", "GitHubRepository": "https://github.com/M1nt-Ch0c0/ai-quota-frame", "ConfigFields": []any{}},
			"capabilities":   map[string]bool{"management_api": true},
		}}
	case "plugin.quiesce", "plugin.shutdown":
		return envelope{OK: true, Result: map[string]any{}}
	case "management.register":
		return envelope{OK: true, Result: map[string]any{
			"resources": []any{
				map[string]string{"Path": "/panel", "Menu": "发送到相框", "Description": "PhotoPainter 相框"},
				map[string]string{"Path": "/frame.wasm"},
				map[string]string{"Path": "/wasm_exec.js"},
			},
			"routes": []any{map[string]string{"Method": "GET", "Path": "/plugins/photopainter/account-subject"}},
		}}
	case "management.handle":
		var request struct {
			Method string
			Path   string
			Query  url.Values
		}
		if json.Unmarshal(raw, &request) != nil {
			return failure("invalid_request", "invalid resource request")
		}
		if request.Method == "GET" && request.Path == "/v0/management/plugins/photopainter/account-subject" {
			return envelope{OK: true, Result: accountSubject(request.Query)}
		}
		status, body, contentType := http.StatusNotFound, []byte("not found"), "text/plain; charset=utf-8"
		encoding := ""
		if request.Method == "GET" {
			switch request.Path {
			case "/v0/resource/plugins/photopainter/panel":
				status, body, contentType = http.StatusOK, panel, "text/html; charset=utf-8"
			case "/v0/resource/plugins/photopainter/frame.wasm":
				status, body, contentType, encoding = http.StatusOK, frameWASM, "application/wasm", "gzip"
			case "/v0/resource/plugins/photopainter/wasm_exec.js":
				status, body, contentType = http.StatusOK, wasmRuntime, "text/javascript; charset=utf-8"
			}
		}
		headers := http.Header{
			"Content-Type": {contentType}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"},
			"Content-Length":          {strconv.Itoa(len(body))},
			"Content-Security-Policy": {"default-src 'none'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; img-src blob: data:; connect-src 'self'; font-src 'self' data:; frame-src 'self'; base-uri 'none'; frame-ancestors 'self'; form-action 'none'"},
			"Permissions-Policy":      {"bluetooth=(self)"},
		}
		if encoding != "" {
			headers.Set("Content-Encoding", encoding)
		}
		return envelope{OK: true, Result: managementResponse{StatusCode: status, Body: body, Headers: headers}}
	default:
		return failure("unsupported_method", "resource-only plugin does not implement this method")
	}
}
func failure(code, message string) envelope {
	return envelope{OK: false, Error: map[string]string{"code": code, "message": message}}
}
