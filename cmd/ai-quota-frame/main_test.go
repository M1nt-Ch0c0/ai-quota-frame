package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFailsChromePreflightBeforeStartingWorkers(t *testing.T) {
	for _, name := range []string{
		"CLIPROXY_BASE_URL", "CLIPROXY_MANAGEMENT_KEY",
		"REFRESH_INTERVAL", "PASSIVE_MAX_AGE", "REQUEST_TIMEOUT", "MAX_CONCURRENCY",
		"CPAMP_BASE_URL", "CPAMP_ADMIN_KEY", "DISPLAY_PROVIDERS",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PHOTOFRAME_PUSH_URL", "http://192.0.2.10/api/push")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", strings.Repeat("p", 64))
	t.Setenv("TZ", "UTC")
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "missing-chrome"))

	if code := run(); code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
}
