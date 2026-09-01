package config

import (
	"strings"
	"testing"
	"time"
)

func TestFromEnvDemoDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("FRAME_ACCESS_TOKEN", "frame-token")

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !configuration.DemoMode || configuration.AllowNoToken {
		t.Fatalf("demo=%v allowNoToken=%v", configuration.DemoMode, configuration.AllowNoToken)
	}
	if configuration.ListenAddr != ":8787" || configuration.RefreshInterval != 5*time.Minute {
		t.Fatalf("defaults = %#v", configuration)
	}
}

func TestFromEnvRequiresIndependentSecrets(t *testing.T) {
	tests := []struct {
		name       string
		management string
		frame      string
		want       string
	}{
		{name: "management key", frame: "frame-token", want: "CLIPROXY_MANAGEMENT_KEY is required"},
		{name: "frame token", management: "management-key", want: "FRAME_ACCESS_TOKEN is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("CLIPROXY_MANAGEMENT_KEY", test.management)
			t.Setenv("FRAME_ACCESS_TOKEN", test.frame)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("FromEnv() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestFromEnvRejectsInvalidBoolean(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "sometimes")
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "parse DEMO_MODE") {
		t.Fatalf("FromEnv() error = %v, want invalid DEMO_MODE", err)
	}
}

func TestFromEnvRejectsReusingManagementKeyOnFrame(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", "same-secret")
	t.Setenv("FRAME_ACCESS_TOKEN", "same-secret")
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("FromEnv() error = %v, want secret separation error", err)
	}
}

func TestNoTokenModeRequiresExplicitLoopbackListener(t *testing.T) {
	for _, test := range []struct {
		name       string
		listenAddr string
		wantError  bool
	}{
		{name: "wildcard default", listenAddr: ":8787", wantError: true},
		{name: "IPv4 loopback", listenAddr: "127.0.0.1:8787"},
		{name: "IPv6 loopback", listenAddr: "[::1]:8787"},
		{name: "localhost", listenAddr: "localhost:8787"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("DEMO_MODE", "true")
			t.Setenv("ALLOW_INSECURE_NO_TOKEN", "true")
			t.Setenv("LISTEN_ADDR", test.listenAddr)
			_, err := FromEnv()
			if (err != nil) != test.wantError {
				t.Fatalf("FromEnv() error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LISTEN_ADDR", "CLIPROXY_BASE_URL", "CLIPROXY_MANAGEMENT_KEY", "FRAME_ACCESS_TOKEN",
		"REFRESH_INTERVAL", "PASSIVE_MAX_AGE", "REQUEST_TIMEOUT", "MAX_CONCURRENCY", "TZ",
		"DEMO_MODE", "ALLOW_INSECURE_NO_TOKEN", "CPAMP_BASE_URL", "CPAMP_ADMIN_KEY",
	} {
		t.Setenv(name, "")
	}
}

func TestFromEnvUsageCollectorSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("FRAME_ACCESS_TOKEN", "frame-token")

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if configuration.CPAMPBaseURL != "" || configuration.CPAMPAdminKey != "" {
		t.Fatalf("usage collector defaults = %#v", configuration)
	}

	t.Setenv("CPAMP_ADMIN_KEY", "admin-key")
	configuration, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() with admin key error = %v", err)
	}
	if configuration.CPAMPBaseURL != "http://127.0.0.1:18317" {
		t.Fatalf("CPAMPBaseURL = %q, want default loopback collector", configuration.CPAMPBaseURL)
	}
}

func TestFromEnvUsageCollectorURLRequiresAdminKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("FRAME_ACCESS_TOKEN", "frame-token")
	t.Setenv("CPAMP_BASE_URL", "http://127.0.0.1:18317/")

	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "CPAMP_ADMIN_KEY is required") {
		t.Fatalf("FromEnv() error = %v, want CPAMP_ADMIN_KEY requirement", err)
	}
}
