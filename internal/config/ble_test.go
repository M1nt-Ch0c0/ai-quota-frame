package config

import "testing"

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
