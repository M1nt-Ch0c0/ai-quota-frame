//go:build photopainter_hardware && (linux || (darwin && cgo))

package ble

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"

	"tinygo.org/x/bluetooth"
)

// Probe only: explicitly install/activate the example before echo-probe, then
// switch back before echo-absent. These checks never switch apps or display.
func TestHardwareApplicationGATT(t *testing.T) {
	mode := os.Getenv("PHOTOFRAME_HARDWARE_TEST")
	if mode != "echo-probe" && mode != "echo-absent" {
		t.Skip("explicit application GATT probe opt-in required")
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	echoService := mustUUID("a4fe1000-1536-4c88-a2ce-32ecdaac0471")
	echoCharacteristic := mustUUID("a4fe1001-1536-4c88-a2ce-32ecdaac0471")
	err := withRadio(ctx, func(adapter *bluetooth.Adapter) error {
		address, err := discover(ctx, adapter, id)
		if err != nil {
			return err
		}
		device, err := adapter.Connect(address, bluetooth.ConnectionParams{ConnectionTimeout: bluetooth.NewDuration(20 * time.Second)})
		if err != nil {
			return err
		}
		defer device.Disconnect()
		services, err := device.DiscoverServices(nil)
		if err != nil {
			return err
		}
		var host, echo *bluetooth.DeviceService
		for i := range services {
			if services[i].UUID() == serviceUUID {
				host = &services[i]
			}
			if services[i].UUID() == echoService {
				echo = &services[i]
			}
		}
		if host == nil {
			return errors.New("permanent host GATT service missing")
		}
		if mode == "echo-absent" {
			if echo != nil {
				return errors.New("application GATT service survived switch-out")
			}
			t.Log("application GATT service removed; permanent discovery/upload service present")
			return nil
		}
		if echo == nil {
			return errors.New("activate ble-echo before probing its custom GATT service")
		}
		appChars, err := echo.DiscoverCharacteristics([]bluetooth.UUID{echoCharacteristic})
		if err != nil || len(appChars) != 1 {
			return errors.New("echo characteristic missing")
		}
		if _, err := readCharacteristic(ctx, appChars[0]); err == nil {
			return errors.New("unauthenticated app read was allowed")
		}
		chars, err := host.DiscoverCharacteristics([]bluetooth.UUID{infoUUID, challengeUUID, rxUUID, txUUID})
		if err != nil || len(chars) != 4 {
			return errors.New("host characteristics missing")
		}
		link := &nativeLink{info: chars[0], challenge: chars[1], rx: chars[2], tx: chars[3], writeSize: 20}
		if mtu, err := link.rx.GetMTU(); err == nil && mtu >= 23 {
			link.writeSize = min(int(mtu)-3, 512)
		}
		c, err := Authenticate(ctx, link, id, token)
		if err != nil {
			return err
		}
		notifications := make(chan []byte, 8)
		if err := appChars[0].EnableNotifications(func(value []byte) {
			select {
			case notifications <- bytes.Clone(value):
			default:
			}
		}); err != nil {
			return err
		}
		defer appChars[0].EnableNotifications(nil)
		value := []byte("native-echo")
		if err := c.AppWrite(ctx, "ble-echo", 0, value); err != nil {
			return err
		}
		got, err := c.AppRead(ctx, "ble-echo", 0)
		if err != nil || !bytes.Equal(got, value) {
			return errors.New("signed echo read mismatch")
		}
		got, err = readCharacteristic(ctx, appChars[0])
		if err != nil || !bytes.Equal(got, value) {
			return errors.New("custom characteristic read mismatch")
		}
		timeout := time.NewTimer(3 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case got := <-notifications:
				if bytes.Equal(got, value) {
					goto notified
				}
			case <-timeout.C:
				return errors.New("echo notification did not arrive")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	notified:
		t.Log("unauthenticated read denied; signed echo, custom GATT read and subscribed notification passed")
		target, err := c.appTarget(ctx, "ble-echo", 0)
		if err != nil {
			return err
		}
		link.rx = appChars[0]
		_, err = c.command(ctx, OpAppWrite, append(bytes.Clone(target), []byte("direct-signed-echo")...))
		link.rx = chars[2]
		if err != nil {
			return err
		}
		got, err = c.AppRead(ctx, "ble-echo", 0)
		if err != nil || string(got) != "direct-signed-echo" {
			return errors.New("signed direct GATT write failed")
		}
		binary.LittleEndian.PutUint32(target, binary.LittleEndian.Uint32(target)-1)
		_, err = c.command(ctx, OpAppWrite, append(target, byte('x')))
		if err = hwExpect(err, 8); err != nil {
			return err
		}
		t.Log("direct custom GATT signed write passed; stale generation rejected")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
