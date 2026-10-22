package config

import (
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"testing"
)

func TestBLETargetWithoutFixedIP(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", validPushToken())
	t.Setenv("PHOTOFRAME_DEVICE_ID", "AA:BB:CC:DD:EE:FF")
	c, err := FromEnv()
	if err != nil || c.PhotoFrameDeviceID != "aabbccddeeff" || c.PhotoFramePushURL != "" {
		t.Fatalf("BLE config: %v", err)
	}
	t.Setenv("PHOTOFRAME_DEVICE_ID", "not-a-device")
	if _, err := FromEnv(); err == nil {
		t.Fatal("invalid stable ID accepted")
	}
}

func TestSavedBindingDoesNotRequireEnvFileChanges(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", validPushToken())
	if err := ble.SaveBinding("001122334455"); err != nil {
		t.Fatal(err)
	}
	c, err := FromEnv()
	if err != nil || c.PhotoFrameDeviceID != "001122334455" {
		t.Fatal(c.PhotoFrameDeviceID, err)
	}
	t.Setenv("PHOTOFRAME_DEVICE_ID", "aabbccddeeff")
	c, err = FromEnv()
	if err != nil || c.PhotoFrameDeviceID != "aabbccddeeff" {
		t.Fatal("explicit ID did not take precedence", err)
	}
}

func TestBLEImageTransportRequiresIdentityAndNoIP(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", validPushToken())
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "ble")
	t.Setenv("PHOTOFRAME_DEVICE_ID", "001122334455")
	c, err := FromEnv()
	if err != nil || c.PhotoFrameTransport != "ble" || c.PhotoFramePushURL != "" {
		t.Fatal(c.PhotoFrameTransport, err)
	}
	t.Setenv("PHOTOFRAME_DEVICE_ID", "")
	t.Setenv("PHOTOFRAME_PUSH_URL", "http://192.168.1.2/api/push")
	if _, err := FromEnv(); err == nil {
		t.Fatal("BLE transport accepted URL without identity")
	}
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "automatic")
	if _, err := FromEnv(); err == nil {
		t.Fatal("unknown transport accepted")
	}
	t.Setenv("PHOTOFRAME_PUSH_TRANSPORT", "")
	c, err = FromEnv()
	if err != nil || c.PhotoFrameTransport != "http" {
		t.Fatal("HTTP default changed", err)
	}
}
