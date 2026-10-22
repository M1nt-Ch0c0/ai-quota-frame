package ble

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type binding struct {
	Version  int    `json:"version"`
	DeviceID string `json:"device_id"`
}

func bindingPath() (string, error) {
	if path := os.Getenv("PHOTOFRAME_BLE_BINDING_FILE"); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("PHOTOFRAME_BLE_BINDING_FILE must be absolute")
		}
		return path, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("user configuration directory unavailable")
	}
	return filepath.Join(directory, "ai-quota-frame", "ble-device.json"), nil
}

// LoadBinding reads only a public device identity, never a token or an IP.
// An explicit PHOTOFRAME_DEVICE_ID takes precedence in config.FromEnv.
func LoadBinding() (string, error) {
	path, err := bindingPath()
	if err != nil {
		if os.Getenv("PHOTOFRAME_BLE_BINDING_FILE") != "" {
			return "", err
		}
		return "", nil // HTTP-only operation does not require a user config directory.
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("cannot read BLE device binding")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 513))
	if err != nil || len(data) > 512 {
		return "", errors.New("invalid BLE device binding size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value binding
	if decoder.Decode(&value) != nil || value.Version != 1 {
		return "", errors.New("invalid BLE device binding; bind again or unbind")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return "", errors.New("invalid BLE device binding; bind again or unbind")
	}
	id, err := NormalizeID(value.DeviceID)
	if err != nil {
		return "", errors.New("invalid BLE device binding ID")
	}
	return id, nil
}

// SaveBinding is called by the CLI only after mutual device authentication.
// Publication is atomic and never edits .env or saves the push token.
func SaveBinding(id string) error {
	id, err := NormalizeID(id)
	if err != nil {
		return err
	}
	path, err := bindingPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create BLE binding directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ble-device-*")
	if err != nil {
		return errors.New("cannot create BLE binding")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = json.NewEncoder(f).Encode(binding{1, id}); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		return errors.New("cannot publish BLE binding")
	}
	return nil
}

func RemoveBinding() error {
	path, err := bindingPath()
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove BLE device binding")
	}
	return nil
}
