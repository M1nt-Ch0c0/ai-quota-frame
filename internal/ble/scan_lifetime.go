package ble

import (
	"context"
	"sync/atomic"
	"time"
)

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
