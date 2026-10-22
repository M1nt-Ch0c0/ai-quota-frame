package ble

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// A native platform adapter may panic on an OS error (for example a null WinRT
// GATT value). Keep that failure inside discovery rather than crashing the pusher.
func containNativeFailure(job func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("native BLE platform call failed; reconnect")
		}
	}()
	return job()
}

// Windows can return an initially unreachable GATT service while establishing
// the maintained connection. Retry only these read-only discovery operations;
// authentication and application commands are never automatically repeated.
func retryDiscovery(ctx context.Context, discover func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = discover(); err == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return err
}

// runScan coordinates the library's blocking Scan/StopScan API. Duplicate
// advertisements and cancellation must not stop a CoreBluetooth scan twice.
func runScan(ctx context.Context, start func(stop func() bool) error, stopNative func() error) error {
	var state atomic.Uint32 // 0 running, 1 stopping, 2 stopped, 3 Scan returned
	finished := make(chan struct{})
	stop := func() bool {
		if !state.CompareAndSwap(0, 1) {
			return state.Load() >= 2
		}
		if err := stopNative(); err != nil {
			state.CompareAndSwap(1, 0)
			return false
		}
		state.CompareAndSwap(1, 2)
		return true
	}
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
				if stop() {
					return
				}
			}
		}
	}()
	defer func() { state.Store(3); close(finished) }()
	return start(stop)
}
