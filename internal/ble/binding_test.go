package ble

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindingContainsOnlyIdentityAndCanBeReplaced(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(directory, "ble-device.json"))
	if id, err := LoadBinding(); err != nil || id != "" {
		t.Fatal(id, err)
	}
	if err := SaveBinding("AA:BB:CC:DD:EE:FF"); err != nil {
		t.Fatal(err)
	}
	if id, err := LoadBinding(); err != nil || id != "aabbccddeeff" {
		t.Fatal(id, err)
	}
	path, _ := bindingPath()
	p, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != "{\"version\":1,\"device_id\":\"aabbccddeeff\"}\n" {
		t.Fatal("binding contained unexpected data")
	}
	if err := SaveBinding("001122334455"); err != nil {
		t.Fatal(err)
	}
	if id, err := LoadBinding(); err != nil || id != "001122334455" {
		t.Fatal(id, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary binding files leaked")
	}
	if err := RemoveBinding(); err != nil {
		t.Fatal(err)
	}
	if id, err := LoadBinding(); err != nil || id != "" {
		t.Fatal(id, err)
	}
}

func TestBindingRejectsCorruptionAndUnknownFields(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(directory, "ble-device.json"))
	path, _ := bindingPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{`, `{"version":2,"device_id":"001122334455"}`, `{"version":1,"device_id":"no"}`, `{"version":1,"device_id":"001122334455","token":"synthetic"}`, `{"version":1,"device_id":"001122334455"} {}`, strings.Repeat("x", 600)} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBinding(); err == nil {
			t.Fatal("bad binding accepted")
		}
	}
}
