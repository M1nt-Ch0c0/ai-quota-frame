package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFromEnvDemoDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	setValidPhotoFrameTarget(t)

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !configuration.DemoMode || configuration.RefreshInterval != 5*time.Minute {
		t.Fatalf("defaults = %#v", configuration)
	}
	if len(configuration.DisplayProviders) != 3 || configuration.DisplayProviders[0].Label != "CODEX" {
		t.Fatalf("default display providers = %#v", configuration.DisplayProviders)
	}
}

func TestFromEnvRequiresManagementKeyOutsideDemo(t *testing.T) {
	clearConfigEnv(t)
	setValidPhotoFrameTarget(t)

	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "CLIPROXY_MANAGEMENT_KEY is required") {
		t.Fatalf("FromEnv() error = %v", err)
	}
}

func TestFromEnvRequiresPhotoFrameTarget(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")

	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "ble pair") {
		t.Fatalf("FromEnv() error = %v", err)
	}
}

func TestFromEnvRejectsInvalidBoolean(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "sometimes")
	setValidPhotoFrameTarget(t)
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "parse DEMO_MODE") {
		t.Fatalf("FromEnv() error = %v, want invalid DEMO_MODE", err)
	}
}

func TestFromEnvPhotoFramePushSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	setValidPhotoFrameTarget(t)

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() configured error = %v", err)
	}
	if configuration.PhotoFramePushURL != "http://192.0.2.10/api/push" ||
		configuration.PhotoFramePushToken != validPushToken() {
		t.Fatalf("push settings = URL %q token-present %v",
			configuration.PhotoFramePushURL, configuration.PhotoFramePushToken != "")
	}
}

func TestFromEnvRejectsInvalidPhotoFramePushURLWithoutExposingToken(t *testing.T) {
	tests := []string{
		"192.0.2.10/api/push",
		"ftp://192.0.2.10/api/push",
		"http://user:password@192.0.2.10/api/push",
		"http://192.0.2.10/",
		"http://192.0.2.10/api/push/",
		"http://192.0.2.10/api/push?force=true",
		"http://192.0.2.10/api/push?",
		"http://192.0.2.10/api/push#fragment",
		"http://192.0.2.10/api/push#",
	}
	for _, pushURL := range tests {
		t.Run(pushURL, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("DEMO_MODE", "true")
			t.Setenv("PHOTOFRAME_PUSH_URL", pushURL)
			secret := validPushToken()
			t.Setenv("PHOTOFRAME_PUSH_TOKEN", secret)
			t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http")
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), "PHOTOFRAME_PUSH_URL") {
				t.Fatalf("FromEnv() error = %v, want push URL validation error", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("configuration error exposed PHOTOFRAME_PUSH_TOKEN")
			}
		})
	}
}

func TestFromEnvRejectsInvalidPhotoFramePushTokenWithoutExposingIt(t *testing.T) {
	tests := []string{
		"short",
		strings.Repeat("x", 129),
		strings.Repeat("x", 31),
		strings.Repeat("x", 31) + "\n",
		strings.Repeat("x", 31) + " ",
		strings.Repeat("x", 64) + " ",
		" " + strings.Repeat("x", 64),
		strings.Repeat("x", 31) + "\x7f",
		"replace-with-a-dedicated-push-token",
		"REPLACE-WITH-A-DEDICATED-PUSH-TOKEN",
	}
	for _, token := range tests {
		t.Run("invalid token", func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("DEMO_MODE", "true")
			t.Setenv("PHOTOFRAME_PUSH_URL", "http://192.0.2.10/api/push")
			t.Setenv("PHOTOFRAME_PUSH_TOKEN", token)
			t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http")
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), "PHOTOFRAME_PUSH_TOKEN") {
				t.Fatalf("FromEnv() error = %v, want token validation error", err)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatal("configuration error exposed PHOTOFRAME_PUSH_TOKEN")
			}
		})
	}
}

func TestFromEnvRequiresDedicatedPhotoFramePushToken(t *testing.T) {
	tests := []struct {
		name       string
		secretName string
	}{
		{name: "CLIProxy management key", secretName: "CLIPROXY_MANAGEMENT_KEY"},
		{name: "usage collector admin key", secretName: "CPAMP_ADMIN_KEY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("DEMO_MODE", "true")
			setValidPhotoFrameTarget(t)
			t.Setenv(test.secretName, validPushToken())

			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), "must be different from "+test.secretName) {
				t.Fatalf("FromEnv() error = %v, want dedicated-token error for %s", err, test.secretName)
			}
			if strings.Contains(err.Error(), validPushToken()) {
				t.Fatal("configuration error exposed reused secret")
			}
		})
	}
}

func TestFromEnvUsageCollectorSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	setValidPhotoFrameTarget(t)

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
	setValidPhotoFrameTarget(t)
	t.Setenv("CPAMP_BASE_URL", "http://127.0.0.1:18317/")

	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "CPAMP_ADMIN_KEY is required") {
		t.Fatalf("FromEnv() error = %v, want CPAMP_ADMIN_KEY requirement", err)
	}
}

func TestFromEnvParsesDisplayProviders(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	setValidPhotoFrameTarget(t)
	t.Setenv("DISPLAY_PROVIDERS", "codex,claude")

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if len(configuration.DisplayProviders) != 2 || configuration.DisplayProviders[1].Label != "CLAUDE" {
		t.Fatalf("DisplayProviders = %#v", configuration.DisplayProviders)
	}
}

func TestFromEnvGoogleServiceAccountReplacesManagementKeys(t *testing.T) {
	clearConfigEnv(t)
	setValidPhotoFrameTarget(t)
	t.Setenv("CLIPROXY_BASE_URL", "https://manager.example.com")
	t.Setenv("CPAMP_BASE_URL", "https://manager.example.com")
	t.Setenv("GOOGLE_SERVICE_ACCOUNT_FILE", "/private/sa.json")
	t.Setenv("GOOGLE_ID_TOKEN_AUDIENCE", "client-id.apps.googleusercontent.com")

	configuration, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if configuration.GoogleServiceAccountFile != "/private/sa.json" || configuration.CPAMPBaseURL == "" {
		t.Fatalf("Google auth config = %#v", configuration)
	}
}

func TestFromEnvGoogleServiceAccountRequiresAudience(t *testing.T) {
	clearConfigEnv(t)
	setValidPhotoFrameTarget(t)
	t.Setenv("GOOGLE_SERVICE_ACCOUNT_FILE", "/private/sa.json")

	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_ID_TOKEN_AUDIENCE is required") {
		t.Fatalf("FromEnv() error = %v", err)
	}
}

func TestFromEnvExplicitManagementKeyIgnoresRetainedGoogleAccount(t *testing.T) {
	clearConfigEnv(t)
	setValidPhotoFrameTarget(t)
	t.Setenv("SOURCE_AUTH_MODE", "management-key")
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", "management-key")
	t.Setenv("GOOGLE_SERVICE_ACCOUNT_FILE", "/retained/sa.json")
	c, err := FromEnv()
	if err != nil || c.SourceAuthMode != "management-key" {
		t.Fatalf("explicit static mode: %v", err)
	}
}

func TestFromEnvRejectsAmbiguousSourceCredentials(t *testing.T) {
	clearConfigEnv(t)
	setValidPhotoFrameTarget(t)
	t.Setenv("CLIPROXY_MANAGEMENT_KEY", "management-key")
	t.Setenv("GOOGLE_SERVICE_ACCOUNT_FILE", "/retained/sa.json")
	t.Setenv("GOOGLE_ID_TOKEN_AUDIENCE", "audience")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "SOURCE_AUTH_MODE is required") {
		t.Fatalf("expected explicit auth selection, got %v", err)
	}
}

func setValidPhotoFrameTarget(t *testing.T) {
	t.Helper()
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "http") // These fixtures exercise the legacy HTTP contract.
	t.Setenv("PHOTOFRAME_PUSH_URL", "http://192.0.2.10/api/push")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", validPushToken())
}

func validPushToken() string {
	return strings.Repeat("p", 64)
}

func clearConfigEnv(t *testing.T) {
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(t.TempDir(), "ble-device.json"))
	t.Helper()
	for _, name := range []string{
		"CLIPROXY_BASE_URL", "CLIPROXY_MANAGEMENT_KEY",
		"REFRESH_INTERVAL", "PASSIVE_MAX_AGE", "REQUEST_TIMEOUT", "MAX_CONCURRENCY", "TZ",
		"DEMO_MODE", "CPAMP_BASE_URL", "CPAMP_ADMIN_KEY", "DISPLAY_PROVIDERS",
		"PHOTOFRAME_PUSH_URL", "PHOTOFRAME_PUSH_TOKEN", "PHOTOFRAME_DEVICE_ID", "PHOTOFRAME_PUSH_TRANSPORT",
		"GOOGLE_SERVICE_ACCOUNT_FILE", "GOOGLE_ID_TOKEN_AUDIENCE", "SOURCE_AUTH_MODE",
	} {
		t.Setenv(name, "")
	}
}
