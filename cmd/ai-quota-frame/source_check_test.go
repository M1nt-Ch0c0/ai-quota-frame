package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sourceCheckEnvironment(t *testing.T, base string) {
	t.Helper()
	for _, name := range []string{
		"REFRESH_INTERVAL", "PASSIVE_MAX_AGE", "REQUEST_TIMEOUT", "MAX_CONCURRENCY",
		"CPAMP_BASE_URL", "CPAMP_ADMIN_KEY", "DISPLAY_PROVIDERS", "FRAME_THEME",
		"GOOGLE_SERVICE_ACCOUNT_FILE", "GOOGLE_ID_TOKEN_AUDIENCE", "PHOTOFRAME_DEVICE_ID",
		"SOURCE_AUTH_MODE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("DEMO_MODE", "false")
	t.Setenv("TZ", "UTC")
	t.Setenv("CLIPROXY_BASE_URL", base)
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", "test-static-secret")
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http")
	t.Setenv("PHOTOFRAME_PUSH_URL", "http://192.0.2.10/api/push")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", strings.Repeat("p", 64))
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http")
}

func TestSourceCheckStaticBypassesRetiredGoogleGatewayWithoutExposingKey(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v0/management/auth-files" || r.Header.Get("Authorization") != "Bearer test-static-secret" {
			t.Errorf("unexpected diagnostic request")
		}
		_, _ = w.Write([]byte(`{"files":[]}`))
	}))
	defer server.Close()
	sourceCheckEnvironment(t, server.URL)
	t.Setenv("GOOGLE_SERVICE_ACCOUNT_FILE", "nonexistent-service-account.json")
	t.Setenv("GOOGLE_ID_TOKEN_AUDIENCE", "test-audience")
	var output, errors bytes.Buffer
	if code := runSourceCheck([]string{"--static"}, &output, &errors); code != 0 {
		t.Fatalf("exit=%d output=%s errors=%s", code, &output, &errors)
	}
	if requests != 1 || strings.Contains(output.String(), "test-static-secret") {
		t.Fatalf("requests=%d or sensitive output", requests)
	}
	var report map[string]any
	if json.Unmarshal(output.Bytes(), &report) != nil || report["auth_mode"] != "management-key" || report["quota_ok"] != true {
		t.Fatalf("unexpected report: %s", &output)
	}
}

func TestSourceCheckDoesNotExposeManagementErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("sensitive-response-body"))
	}))
	defer server.Close()
	sourceCheckEnvironment(t, server.URL)
	var output, errors bytes.Buffer
	if code := runSourceCheck(nil, &output, &errors); code != 1 {
		t.Fatalf("exit=%d, want source failure", code)
	}
	if strings.Contains(output.String(), "sensitive-response-body") || strings.Contains(output.String(), "test-static-secret") || !strings.Contains(output.String(), "HTTP 401") {
		t.Fatalf("unexpected diagnostic output: %s", &output)
	}
}
