//go:build photopainter_hardware && windows

package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"

	winbt "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
)

// Hold an uncommitted receive buffer until host idle expiry. Observe Windows'
// physical connection status before releasing any client objects, then prove
// fresh authentication, buffer cleanup and manager admission without staging.
func TestHardwareIdleExpiry(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "idle-expiry" {
		t.Skip("explicit idle-expiry hardware opt-in required")
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	begin := make([]byte, 68)
	binary.LittleEndian.PutUint32(begin, MaxPackageSize)
	app, _ := encodeApp("ble-echo")
	copy(begin[36:], app)
	var before hardwareState
	err := WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		var err error
		before, err = hwStatus(ctx, c)
		if err != nil {
			return err
		}
		for _, app := range before.Apps {
			if app.ID == "ble-echo" {
				return errors.New("ble-echo already exists; refusing to target it")
			}
		}
		hwMemory(t, "before-idle-expiry", before)
		if _, err = c.command(ctx, OpBegin, begin); err != nil {
			return err
		}
		conn := c.link.(*nativeLink).info.(*winCharacteristic).conn
		start := time.Now()
		t.Log("receive admission held; observing physical disconnect without further GATT traffic")
		for time.Since(start) < 75*time.Second {
			status, err := conn.device.GetConnectionStatus()
			if err != nil {
				return err
			}
			if status == winbt.BluetoothConnectionStatusDisconnected {
				if time.Since(start) < 55*time.Second {
					return errors.New("connection dropped before the idle deadline")
				}
				t.Logf("host physical disconnect observed after %.1fs", time.Since(start).Seconds())
				return nil
			}
			if err = wait(ctx, 200*time.Millisecond); err != nil {
				return err
			}
		}
		return errors.New("expired connection remained physically connected")
	})
	if err != nil {
		t.Fatal(err)
	}
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		after, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "after-idle-expiry", after)
		if after.UptimeMS < before.UptimeMS+55000 || after.ResetReason != before.ResetReason {
			return errors.New("device rebooted during idle-expiry test")
		}
		if after.Loaded != before.Loaded || len(after.Apps) != len(before.Apps) || after.FreePSRAM+8192 < before.FreePSRAM {
			return errors.New("idle expiry did not preserve catalog or release receive buffer")
		}
		if _, err = c.command(ctx, OpBegin, begin); err != nil {
			return err
		}
		_, err = c.command(ctx, OpAbort, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("fresh authentication, receive buffer release and manager admission verified")
}
