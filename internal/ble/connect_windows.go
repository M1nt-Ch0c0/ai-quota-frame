package ble

// Keep ownership of WinRT objects on the radio worker. In particular, close and
// release every characteristic/service before the session and device, and do
// not free a session from a connection-status callback. Uncached service
// discovery initiates the actual connection; MaintainConnection is only a hint.
import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	winbt "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	gatt "github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
	"tinygo.org/x/bluetooth"
)

// The worker retains ownership until an uncancellable WinRT operation returns.
// withRadio bounds the caller's wait and prevents overlapping radio workers.
func winResult(op *foundation.IAsyncOperation, opErr error, signature string) (unsafe.Pointer, error) {
	if opErr != nil {
		return nil, opErr
	}
	if op == nil {
		return nil, errors.New("WinRT returned no operation")
	}
	defer op.Release()
	done := make(chan foundation.AsyncStatus, 1)
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, signature)
	handler := foundation.NewAsyncOperationCompletedHandler(ole.NewGUID(iid), func(_ *foundation.AsyncOperationCompletedHandler, _ *foundation.IAsyncOperation, status foundation.AsyncStatus) {
		done <- status
	})
	defer handler.Release()
	if err := op.SetCompleted(handler); err != nil {
		return nil, err
	}
	if status := <-done; status != foundation.AsyncStatusCompleted {
		return nil, fmt.Errorf("WinRT operation failed with status %d", status)
	}
	return op.GetResults()
}

type winConnection struct {
	device   *winbt.BluetoothLEDevice
	session  *gatt.GattSession
	service  *gatt.GattDeviceService
	chars    []*gatt.GattCharacteristic
	priority *winbt.BluetoothLEPreferredConnectionParametersRequest
	once     sync.Once
}

func (c *winConnection) close() {
	c.once.Do(func() {
		if c.priority != nil {
			_ = c.priority.Close()
			c.priority.Release()
		}
		for _, ch := range c.chars {
			ch.Release()
		}
		if c.service != nil {
			_ = c.service.Close()
			c.service.Release()
		}
		if c.session != nil {
			_ = c.session.SetMaintainConnection(false)
			_ = c.session.Close()
			c.session.Release()
		}
		if c.device != nil {
			_ = c.device.Close()
			c.device.Release()
		}
	})
}
func (c *winConnection) throughput() error {
	if c.priority != nil {
		return nil
	}
	params, err := winbt.BluetoothLEPreferredConnectionParametersGetThroughputOptimized()
	if err != nil {
		return err
	}
	defer params.Release()
	request, err := c.device.RequestPreferredConnectionParameters(params)
	if err != nil {
		return err
	}
	status, err := request.GetStatus()
	if err != nil || status != winbt.BluetoothLEPreferredConnectionParametersRequestStatusSuccess {
		_ = request.Close()
		request.Release()
		return errors.New("Windows throughput preference unavailable")
	}
	c.priority = request
	return nil
}

func openNativeLink(ctx context.Context, _ *bluetooth.Adapter, address bluetooth.Address) (_ *nativeLink, _ func(), resultErr error) {
	conn := &winConnection{}
	ready := false
	defer func() {
		if !ready {
			conn.close()
		}
	}()
	var numeric uint64
	for i, b := range address.MAC {
		numeric |= uint64(b) << (8 * i)
	}
	op, err := winbt.BluetoothLEDeviceFromBluetoothAddressAsync(numeric)
	p, err := winResult(op, err, winbt.SignatureBluetoothLEDevice)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, errors.New("Windows BLE device unavailable")
	}
	conn.device = (*winbt.BluetoothLEDevice)(p)
	id, err := conn.device.GetBluetoothDeviceId()
	if err != nil {
		return nil, nil, err
	}
	defer id.Release()
	op, err = gatt.GattSessionFromDeviceIdAsync(id)
	p, err = winResult(op, err, gatt.SignatureGattSession)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, errors.New("Windows GATT session unavailable")
	}
	conn.session = (*gatt.GattSession)(p)
	if err = conn.session.SetMaintainConnection(true); err != nil {
		return nil, nil, err
	}
	if err = retryDiscovery(ctx, conn.discoverService); err != nil {
		return nil, nil, err
	}
	var chars []nativeCharacteristic
	if err = retryDiscovery(ctx, func() error { var err error; chars, err = conn.discoverCharacteristics(); return err }); err != nil {
		return nil, nil, err
	}
	link := &nativeLink{info: chars[0], challenge: chars[1], rx: chars[2], tx: chars[3], writeSize: 20, requestThroughput: conn.throughput}
	if mtu, err := conn.session.GetMaxPduSize(); err == nil && mtu >= 23 {
		link.writeSize = min(int(mtu)-3, 512)
	}
	ready = true
	return link, conn.close, nil
}

func (c *winConnection) discoverService() error {
	op, err := c.device.GetGattServicesWithCacheModeAsync(winbt.BluetoothCacheModeUncached)
	p, err := winResult(op, err, gatt.SignatureGattDeviceServicesResult)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("Windows GATT service result missing")
	}
	result := (*gatt.GattDeviceServicesResult)(p)
	defer result.Release()
	status, err := result.GetStatus()
	if err != nil {
		return err
	}
	if status != gatt.GattCommunicationStatusSuccess {
		return fmt.Errorf("Windows service discovery failed: %d", status)
	}
	vector, err := result.GetServices()
	if err != nil {
		return err
	}
	defer vector.Release()
	size, err := vector.GetSize()
	if err != nil {
		return err
	}
	for i := uint32(0); i < size; i++ {
		p, err := vector.GetAt(i)
		if err != nil {
			return err
		}
		service := (*gatt.GattDeviceService)(p)
		uuid, err := service.GetUuid()
		if err == nil && bluetooth.GUIDToUUID(uuid) == serviceUUID && c.service == nil {
			c.service = service
			return nil
		} else {
			_ = service.Close()
			service.Release()
		}
		if err != nil {
			return err
		}
	}
	if c.service == nil {
		return errors.New("PhotoPainter BLE service unavailable")
	}
	return nil
}

func (c *winConnection) discoverCharacteristics() ([]nativeCharacteristic, error) {
	op, err := c.service.GetCharacteristicsWithCacheModeAsync(winbt.BluetoothCacheModeUncached)
	p, err := winResult(op, err, gatt.SignatureGattCharacteristicsResult)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("Windows characteristic result missing")
	}
	result := (*gatt.GattCharacteristicsResult)(p)
	defer result.Release()
	status, err := result.GetStatus()
	if err != nil {
		return nil, err
	}
	if status != gatt.GattCommunicationStatusSuccess {
		return nil, fmt.Errorf("Windows characteristic discovery failed: %d", status)
	}
	vector, err := result.GetCharacteristics()
	if err != nil {
		return nil, err
	}
	defer vector.Release()
	size, err := vector.GetSize()
	if err != nil {
		return nil, err
	}
	wanted := []bluetooth.UUID{infoUUID, challengeUUID, rxUUID, txUUID}
	chars := make([]nativeCharacteristic, 4)
	for i := uint32(0); i < size; i++ {
		p, err := vector.GetAt(i)
		if err != nil {
			return nil, err
		}
		ch := (*gatt.GattCharacteristic)(p)
		c.chars = append(c.chars, ch)
		uuid, err := ch.GetUuid()
		if err != nil {
			return nil, err
		}
		for j, w := range wanted {
			if bluetooth.GUIDToUUID(uuid) == w {
				chars[j] = &winCharacteristic{conn: c, value: ch}
			}
		}
	}
	for _, ch := range chars {
		if ch == nil {
			return nil, errors.New("PhotoPainter BLE characteristic unavailable")
		}
	}
	return chars, nil
}

type winCharacteristic struct {
	conn  *winConnection
	value *gatt.GattCharacteristic
}

func (c *winCharacteristic) GetMTU() (uint16, error) { return c.conn.session.GetMaxPduSize() }
func (c *winCharacteristic) Read(destination []byte) (int, error) {
	op, err := c.value.ReadValueWithCacheModeAsync(winbt.BluetoothCacheModeUncached)
	p, err := winResult(op, err, gatt.SignatureGattReadResult)
	if err != nil {
		return 0, err
	}
	if p == nil {
		return 0, errors.New("Windows GATT read result missing")
	}
	result := (*gatt.GattReadResult)(p)
	defer result.Release()
	status, err := result.GetStatus()
	if err != nil {
		return 0, err
	}
	if status != gatt.GattCommunicationStatusSuccess {
		return 0, fmt.Errorf("Windows GATT read failed: %d", status)
	}
	buffer, err := result.GetValue()
	if err != nil {
		return 0, err
	}
	if buffer == nil {
		return 0, errors.New("Windows GATT value missing")
	}
	defer buffer.Release()
	size, err := buffer.GetLength()
	if err != nil {
		return 0, err
	}
	if size > uint32(len(destination)) {
		return 0, errors.New("oversize Windows GATT value")
	}
	if size == 0 {
		return 0, nil
	}
	reader, err := streams.DataReaderFromBuffer(buffer)
	if err != nil {
		return 0, err
	}
	defer reader.Release()
	data, err := reader.ReadBytes(size)
	if err != nil {
		return 0, err
	}
	return copy(destination, data), nil
}
func (c *winCharacteristic) Write(data []byte) (int, error) {
	writer, err := streams.NewDataWriter()
	if err != nil {
		return 0, err
	}
	defer writer.Release()
	if err = writer.WriteBytes(uint32(len(data)), data); err != nil {
		return 0, err
	}
	buffer, err := writer.DetachBuffer()
	if err != nil {
		return 0, err
	}
	defer buffer.Release()
	op, err := c.value.WriteValueWithOptionAsync(buffer, gatt.GattWriteOptionWriteWithResponse)
	p, err := winResult(op, err, gatt.SignatureGattCommunicationStatus)
	if err != nil {
		return 0, err
	}
	if status := gatt.GattCommunicationStatus(uintptr(p)); status != gatt.GattCommunicationStatusSuccess {
		return 0, fmt.Errorf("Windows GATT write failed: %d", status)
	}
	return len(data), nil
}
