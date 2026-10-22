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
