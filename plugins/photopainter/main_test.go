package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceOnlyRegistration(t *testing.T) {
	r := handleMethod("plugin.register", nil)
	data, _ := json.Marshal(r.Result)
	if !r.OK || !strings.Contains(string(data), `"management_api":true`) || strings.Contains(string(data), `"executor":true`) {
		t.Fatal("not resource-only registration")
	}
	r = handleMethod("management.register", nil)
	data, _ = json.Marshal(r.Result)
	if !r.OK || !strings.Contains(string(data), `"Path":"/panel"`) {
		t.Fatal("missing menu resource")
	}
}
func TestResourceExactPathAndNoSecrets(t *testing.T) {
	for _, path := range []string{"/v0/resource/plugins/photopainter/panel", "/v0/resource/plugins/photopainter/../panel", "/v0/management/config"} {
		raw, _ := json.Marshal(map[string]string{"Method": "GET", "Path": path})
		r := handleMethod("management.handle", raw)
		response := r.Result.(managementResponse)
		if path == "/v0/resource/plugins/photopainter/panel" {
			if response.StatusCode != 200 || !strings.Contains(string(response.Body), "<html") || response.Headers.Get("Cache-Control") != "no-store" {
				t.Fatal("panel response invalid")
			}
		} else if response.StatusCode != 404 {
			t.Fatal("unexpected resource exposure")
		}
	}
	if handleMethod("host.auth.list", nil).OK {
		t.Fatal("must not implement host credential methods")
	}
}

func TestSharedEngineResourceRoutes(t *testing.T) {
	for _, entry := range []struct {
		path, contentType, encoding string
		bytes                       []byte
	}{
		{"/v0/resource/plugins/photopainter/frame.wasm", "application/wasm", "gzip", frameWASM},
		{"/v0/resource/plugins/photopainter/wasm_exec.js", "text/javascript; charset=utf-8", "", wasmRuntime},
	} {
		raw, _ := json.Marshal(map[string]string{"Method": "GET", "Path": entry.path})
		response := handleMethod("management.handle", raw).Result.(managementResponse)
		if response.StatusCode != 200 || response.Headers.Get("Content-Type") != entry.contentType || response.Headers.Get("Content-Encoding") != entry.encoding || len(response.Body) != len(entry.bytes) {
			t.Fatalf("invalid engine resource: %s", entry.path)
		}
		if !strings.Contains(response.Headers.Get("Content-Security-Policy"), "wasm-unsafe-eval") {
			t.Fatal("shared Go engine blocked by policy")
		}
	}
}
