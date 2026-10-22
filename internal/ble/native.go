//go:build windows || linux || (darwin && cgo)

package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

var (
	serviceUUID   = mustUUID("7b9d0001-6c82-4f31-9a48-6f5061696e74")
	infoUUID      = mustUUID("7b9d0002-6c82-4f31-9a48-6f5061696e74")
	challengeUUID = mustUUID("7b9d0003-6c82-4f31-9a48-6f5061696e74")
	rxUUID        = mustUUID("7b9d0004-6c82-4f31-9a48-6f5061696e74")
	txUUID        = mustUUID("7b9d0005-6c82-4f31-9a48-6f5061696e74")
	// A blocked native OS call keeps this slot until it returns, so timeouts
	// cannot create unbounded goroutines or concurrent adapter operations.
	radioSlot = make(chan struct{}, 1)
)

func mustUUID(value string) bluetooth.UUID {
	id, err := bluetooth.ParseUUID(value)
	if err != nil {
		panic(err)
	}
	return id
}

type nativeLink struct {
	info, challenge, rx, tx bluetooth.DeviceCharacteristic
	writeSize               int
}

func readCharacteristic(ctx context.Context, c bluetooth.DeviceCharacteristic) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := make([]byte, 512)
	n, err := c.Read(p)
	if err != nil {
		return nil, errors.New("BLE characteristic read failed")
	}
	if n < 0 || n > len(p) {
		return nil, errors.New("oversize BLE characteristic")
	}
	return p[:n], nil
}
func (l *nativeLink) Info(ctx context.Context) ([]byte, error) {
	return readCharacteristic(ctx, l.info)
}
func (l *nativeLink) Challenge(ctx context.Context) ([]byte, error) {
	// The ESP worker publishes a fresh nonce after the GAP connection event.
	for i := 0; i < 20; i++ {
		p, err := readCharacteristic(ctx, l.challenge)
		if err == nil && len(p) == 32 {
			return p, nil
		}
		if err := wait(ctx, 50*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("BLE challenge not ready")
}
func (l *nativeLink) Exchange(ctx context.Context, request []byte) ([]byte, error) {
	if len(request) == 0 || len(request) > 512 {
		return nil, errors.New("invalid BLE frame size")
	}
	for offset := 0; offset < len(request); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+l.writeSize-4, len(request))
		fragment := make([]byte, 4, 4+end-offset)
		binary.LittleEndian.PutUint16(fragment, uint16(offset))
		binary.LittleEndian.PutUint16(fragment[2:], uint16(len(request)))
		fragment = append(fragment, request[offset:end]...)
		n, err := l.rx.Write(fragment)
		if err != nil || n != len(fragment) {
			return nil, errors.New("BLE fragment write failed; reconnect and retry the whole package")
		}
		offset = end
	}
	for {
		p, err := readCharacteristic(ctx, l.tx)
		if err != nil {
			return nil, err
		}
		if len(p) != 1 || p[0] != 0 {
			return p, nil
		}
		if err := wait(ctx, 25*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// withRadio bounds the caller's wait even if a platform's BLE API does not
// support cancellation. The native operation owns its resources until it exits.
func withRadio(ctx context.Context, job func(*bluetooth.Adapter) error) error {
	select {
	case radioSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		defer func() { <-radioSlot }()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		adapter := bluetooth.DefaultAdapter
		cleanup, err := enableAdapter(adapter)
		if err != nil {
			done <- errors.New("BLE adapter unavailable; enable Bluetooth and grant OS permissions")
			return
		}
		defer cleanup()
		if err := ctx.Err(); err != nil {
			done <- err
			return
		}
		done <- job(adapter)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func discover(ctx context.Context, adapter *bluetooth.Adapter, id string) (bluetooth.Address, error) {
	var address bluetooth.Address
	var found bool
	var mu sync.Mutex
	finished := make(chan struct{})
	// StopScan is the library's cancellation interface. Keep retrying during
	// startup so a deadline before the native scan starts is not lost.
	go func() {
		select {
		case <-ctx.Done():
		case <-finished:
			return
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-ticker.C:
				_ = adapter.StopScan()
			}
		}
	}()
	err := adapter.Scan(func(a *bluetooth.Adapter, result bluetooth.ScanResult) {
		for _, data := range result.ManufacturerData() {
			if data.CompanyID != 0xffff {
				continue
			}
			info, err := ParseInfo(data.Data)
			if err != nil || info.ID != id {
				continue
			}
			mu.Lock()
			if !found {
				address = result.Address
				found = true
			}
			mu.Unlock()
			_ = a.StopScan()
		}
	})
	close(finished)
	if ctx.Err() != nil {
		return address, ctx.Err()
	}
	if err != nil {
		return address, errors.New("BLE scan failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if !found {
		return address, errors.New("BLE device not found")
	}
	return address, nil
}

// Scan returns untrusted discovery metadata only. The operation is bounded by
// ctx; no authentication or device mutation is performed.
func Scan(ctx context.Context) ([]DeviceInfo, error) {
	var mu sync.Mutex
	devices := map[string]DeviceInfo{}
	err := withRadio(ctx, func(adapter *bluetooth.Adapter) error {
		finished := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-finished:
				return
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-finished:
					return
				case <-ticker.C:
					_ = adapter.StopScan()
				}
			}
		}()
		defer close(finished)
		return adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
			for _, data := range result.ManufacturerData() {
				if data.CompanyID != 0xffff {
					continue
				}
				info, err := ParseInfo(data.Data)
				if err != nil {
					continue
				}
				mu.Lock()
				devices[info.ID] = info
				mu.Unlock()
			}
		})
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	result := make([]DeviceInfo, 0, len(devices))
	for _, info := range devices {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// WithClient scans by the stable ID, authenticates, runs one bounded operation,
// then disconnects. It does not read or modify .env or start a listener.
func WithClient(ctx context.Context, id, token string, job func(context.Context, *Client) error) error {
	id, err := NormalizeID(id)
	if err != nil {
		return err
	}
	return withRadio(ctx, func(adapter *bluetooth.Adapter) error {
		scanCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		address, err := discover(scanCtx, adapter, id)
		cancel()
		if err != nil {
			return err
		}
		device, err := adapter.Connect(address, bluetooth.ConnectionParams{ConnectionTimeout: bluetooth.NewDuration(10 * time.Second)})
		if err != nil {
			return errors.New("BLE connection failed")
		}
		defer device.Disconnect()
		services, err := device.DiscoverServices([]bluetooth.UUID{serviceUUID})
		if err != nil || len(services) != 1 {
			return errors.New("PhotoPainter BLE service unavailable")
		}
		chars, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{infoUUID, challengeUUID, rxUUID, txUUID})
		if err != nil || len(chars) != 4 {
			return errors.New("PhotoPainter BLE characteristics unavailable")
		}
		link := &nativeLink{info: chars[0], challenge: chars[1], rx: chars[2], tx: chars[3], writeSize: 20}
		if mtu, err := link.rx.GetMTU(); err == nil && mtu >= 23 {
			link.writeSize = min(int(mtu)-3, 512)
		}
		client, err := Authenticate(ctx, link, id, token)
		if err != nil {
			return err
		}
		return job(ctx, client)
	})
}

func DiscoverPushURL(ctx context.Context, id, token string) (string, error) {
	// Job writes a channel rather than a captured variable: a timed-out native
	// operation may finish after this function has returned.
	value := make(chan string, 1)
	err := WithClient(ctx, id, token, func(_ context.Context, c *Client) error {
		url, err := c.PushURL()
		if err == nil {
			value <- url
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("BLE discovery: %w", err)
	}
	return <-value, nil
}
