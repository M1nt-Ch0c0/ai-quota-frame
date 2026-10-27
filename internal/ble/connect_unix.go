//go:build linux || (darwin && cgo)

package ble

import (
	"context"
	"errors"
	"fmt"
	"time"
	"tinygo.org/x/bluetooth"
)

func openNativeLink(ctx context.Context, adapter *bluetooth.Adapter, address bluetooth.Address) (*nativeLink, func(), error) {
	device, err := adapter.Connect(address, bluetooth.ConnectionParams{
		ConnectionTimeout: bluetooth.NewDuration(20 * time.Second),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("BLE connection failed: %w", err)
	}
	ready := false
	defer func() {
		if !ready {
			_ = device.Disconnect()
		}
	}()
	var services []bluetooth.DeviceService
	if err := retryDiscovery(ctx, func() error {
		var err error
		services, err = device.DiscoverServices([]bluetooth.UUID{serviceUUID})
		if err != nil {
			return fmt.Errorf("PhotoPainter BLE service unavailable: %w", err)
		}
		if len(services) != 1 {
			return errors.New("PhotoPainter BLE service unavailable")
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	var chars []bluetooth.DeviceCharacteristic
	if err := retryDiscovery(ctx, func() error {
		var err error
		chars, err = services[0].DiscoverCharacteristics([]bluetooth.UUID{infoUUID, challengeUUID, rxUUID, txUUID})
		if err != nil {
			return fmt.Errorf("PhotoPainter BLE characteristics unavailable: %w", err)
		}
		if len(chars) != 4 {
			return errors.New("PhotoPainter BLE characteristics unavailable")
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	link := &nativeLink{info: chars[0], challenge: chars[1], rx: chars[2], tx: chars[3], writeSize: 20}
	configureCoC(link, address.String())
	link.requestThroughput = func() error {
		return device.RequestConnectionParams(bluetooth.ConnectionParams{Priority: bluetooth.ConnectionPriorityThroughput})
	}
	if mtu, err := link.rx.GetMTU(); err == nil && mtu >= 23 {
		link.writeSize = min(int(mtu)-3, 512)
	}
	ready = true
	return link, func() { _ = device.Disconnect() }, nil
}
