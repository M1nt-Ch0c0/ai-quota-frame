//go:build photopainter_hardware && windows

package ble

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	winbt "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	gatt "github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
	"tinygo.org/x/bluetooth"
)

// Like the other platform probe, this neither selects an app nor displays.
func TestHardwareApplicationGATT(t *testing.T) {
	mode := os.Getenv("PHOTOFRAME_HARDWARE_TEST")
	if mode != "echo-probe" && mode != "echo-absent" {
		t.Skip("explicit application GATT probe opt-in required")
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := withNativeLink(ctx, id, func(link *nativeLink) error {
		conn := link.info.(*winCharacteristic).conn
		service, ch, err := hwWinEcho(conn)
		if err != nil {
			return err
		}
		if service != nil {
			defer func() { _ = service.Close(); service.Release() }()
		}
		if ch != nil {
			defer ch.Release()
		}
		if mode == "echo-absent" {
			if service != nil {
				return errors.New("application service survived switch-out")
			}
			t.Log("application GATT service removed; permanent discovery/upload service present")
			return nil
		}
		if ch == nil {
			return errors.New("activate ble-echo before probing")
		}
		custom := &winCharacteristic{conn: conn, value: ch}
		if _, err := readCharacteristic(ctx, custom); err == nil {
			return errors.New("unauthenticated app read allowed")
		}
		c, err := Authenticate(ctx, link, id, token)
		if err != nil {
			return err
		}
		notifications := make(chan []byte, 8)
		stop, err := hwWinSubscribe(ch, notifications)
		if err != nil {
			return err
		}
		defer stop()
		value := []byte("native-echo")
		if err = c.AppWrite(ctx, "ble-echo", 0, value); err != nil {
			return err
		}
		got, err := c.AppRead(ctx, "ble-echo", 0)
		if err != nil || !bytes.Equal(got, value) {
			return errors.New("signed echo read mismatch")
		}
		got, err = readCharacteristic(ctx, custom)
		if err != nil || !bytes.Equal(got, value) {
			return errors.New("custom GATT read mismatch")
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
		original := link.rx
		link.rx = custom
		_, err = c.command(ctx, OpAppWrite, append(bytes.Clone(target), []byte("direct-signed-echo")...))
		link.rx = original
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

func hwWinEcho(conn *winConnection) (_ *gatt.GattDeviceService, _ *gatt.GattCharacteristic, resultErr error) {
	op, err := conn.device.GetGattServicesWithCacheModeAsync(winbt.BluetoothCacheModeUncached)
	p, err := winResult(op, err, gatt.SignatureGattDeviceServicesResult)
	if err != nil || p == nil {
		return nil, nil, errors.New("GATT service discovery failed")
	}
	result := (*gatt.GattDeviceServicesResult)(p)
	defer result.Release()
	status, err := result.GetStatus()
	if err != nil || status != gatt.GattCommunicationStatusSuccess {
		return nil, nil, errors.New("GATT service status failed")
	}
	vector, err := result.GetServices()
	if err != nil {
		return nil, nil, err
	}
	defer vector.Release()
	size, err := vector.GetSize()
	if err != nil {
		return nil, nil, err
	}
	want := mustUUID("a4fe1000-1536-4c88-a2ce-32ecdaac0471")
	var service *gatt.GattDeviceService
	for i := uint32(0); i < size; i++ {
		p, err := vector.GetAt(i)
		if err != nil {
			return nil, nil, err
		}
		s := (*gatt.GattDeviceService)(p)
		uuid, e := s.GetUuid()
		if e == nil && bluetooth.GUIDToUUID(uuid) == want {
			service = s
			break
		}
		// The permanent service is already owned by conn. Release this extra
		// reference without closing that shared service during the live session.
		s.Release()
		if e != nil {
			return nil, nil, e
		}
	}
	if service == nil {
		return nil, nil, nil
	}
	defer func() {
		if resultErr != nil {
			_ = service.Close()
			service.Release()
		}
	}()
	op, err = service.GetCharacteristicsWithCacheModeAsync(winbt.BluetoothCacheModeUncached)
	p, err = winResult(op, err, gatt.SignatureGattCharacteristicsResult)
	if err != nil || p == nil {
		return nil, nil, errors.New("custom characteristic discovery failed")
	}
	chars := (*gatt.GattCharacteristicsResult)(p)
	defer chars.Release()
	status, err = chars.GetStatus()
	if err != nil || status != gatt.GattCommunicationStatusSuccess {
		return nil, nil, errors.New("custom characteristic status failed")
	}
	items, err := chars.GetCharacteristics()
	if err != nil {
		return nil, nil, err
	}
	defer items.Release()
	n, err := items.GetSize()
	if err != nil || n != 1 {
		return nil, nil, errors.New("unexpected echo characteristic count")
	}
	p, err = items.GetAt(0)
	if err != nil {
		return nil, nil, err
	}
	return service, (*gatt.GattCharacteristic)(p), nil
}

func hwWinSubscribe(ch *gatt.GattCharacteristic, notifications chan<- []byte) (func(), error) {
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler, gatt.SignatureGattCharacteristic, gatt.SignatureGattValueChangedEventArgs)
	handler := foundation.NewTypedEventHandler(ole.NewGUID(iid), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		args := (*gatt.GattValueChangedEventArgs)(arg)
		buffer, err := args.GetCharacteristicValue()
		if err != nil || buffer == nil {
			return
		}
		defer buffer.Release()
		size, err := buffer.GetLength()
		if err != nil || size > 128 {
			return
		}
		reader, err := streams.DataReaderFromBuffer(buffer)
		if err != nil {
			return
		}
		defer reader.Release()
		data, err := reader.ReadBytes(size)
		if err == nil {
			select {
			case notifications <- data:
			default:
			}
		}
	})
	token, err := ch.AddValueChanged(handler)
	if err != nil {
		handler.Release()
		return nil, err
	}
	stop := func() {
		op, err := ch.WriteClientCharacteristicConfigurationDescriptorAsync(gatt.GattClientCharacteristicConfigurationDescriptorValueNone)
		_, _ = winResult(op, err, gatt.SignatureGattCommunicationStatus)
		_ = ch.RemoveValueChanged(token)
		handler.Release()
	}
	op, err := ch.WriteClientCharacteristicConfigurationDescriptorAsync(gatt.GattClientCharacteristicConfigurationDescriptorValueNotify)
	p, err := winResult(op, err, gatt.SignatureGattCommunicationStatus)
	if err != nil || gatt.GattCommunicationStatus(uintptr(p)) != gatt.GattCommunicationStatusSuccess {
		stop()
		return nil, errors.New("notification subscription failed")
	}
	return stop, nil
}
