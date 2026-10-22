package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBLECLIValidatesBeforeOpeningRadio(t *testing.T) {
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(t.TempDir(), "ble-device.json"))
	t.Setenv("PHOTOFRAME_PUSH_TOKEN", "")
	t.Setenv("PHOTOFRAME_DEVICE_ID", "")
	for _, args := range [][]string{nil, {"unknown"}, {"status"}, {"status", "--device-id", "001122334455"}, {"install", "--file", filepath.Join(t.TempDir(), "missing.pkg")}} {
		var out, errout bytes.Buffer
		if code := runBLE(args, &out, &errout); code != 2 {
			t.Fatalf("%v exited %d", args, code)
		}
	}
}
func TestBLEInspectRejectsInvalidPackageOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pkg")
	if err := os.WriteFile(path, []byte("bad package"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := runBLE([]string{"inspect", "--file", path}, &out, &errout); code != 2 || !strings.Contains(errout.String(), "invalid PhotoPainter package") {
		t.Fatalf("inspect: %d %s", code, errout.String())
	}
}

func TestBLEPushRejectsInvalidPNGBeforeOpeningRadio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.png")
	if err := os.WriteFile(path, []byte("not a PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := runBLE([]string{"push", "--file", path}, &out, &errout); code != 2 || !strings.Contains(errout.String(), "PNG") {
		t.Fatalf("push: %d %s", code, errout.String())
	}
}
