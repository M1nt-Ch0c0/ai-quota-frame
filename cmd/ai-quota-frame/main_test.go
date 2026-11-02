package main

import (
	"bytes"
	"io"
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
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", strings.Repeat("p", 64))
	t.Setenv("TZ", "UTC")
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "missing-chrome"))

	if code := run([]string{"daemon"}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("daemon exit code = %d, want 2", code)
	}
}

func TestBareInvocationPrintsHelpWithoutStartingDaemon(t *testing.T) {
	// A missing browser would fail the daemon preflight; help must not reach it.
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "missing-chrome"))
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "ai-quota-frame daemon") || stderr.Len() != 0 {
			t.Fatalf("run(%q) = %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestUnknownCommandAndDaemonArgumentsAreRejected(t *testing.T) {
	for _, args := range [][]string{{"deamon"}, {"daemon", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("run(%q) = %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
	var stdout bytes.Buffer
	if code := run([]string{"daemon", "--help"}, &stdout, io.Discard); code != 0 || !strings.Contains(stdout.String(), "usage: ai-quota-frame daemon") {
		t.Fatalf("daemon --help = %d, %q", code, stdout.String())
	}
}
