//go:build !windows && !linux && !(darwin && cgo)

package ble

import (
	"context"
	"errors"
)

func WithClient(context.Context, string, string, func(context.Context, *Client) error) error {
	return errors.New("BLE requires Windows, Linux with BlueZ, or a macOS build with CGO enabled")
}
func DiscoverPushURL(context.Context, string, string) (string, error) {
	return "", errors.New("BLE is unavailable in this build")
}

func Scan(context.Context) ([]DeviceInfo, error) {
	return nil, errors.New("BLE is unavailable in this build")
}

func PairDevice(context.Context, string, string, func(PairingProgress)) (*Bond, error) {
	return nil, errors.New("BLE pairing is unavailable in this build")
}
func PairingStatus(context.Context, string) (PairingInfo, error) {
	return PairingInfo{}, errors.New("BLE pairing is unavailable in this build")
}
