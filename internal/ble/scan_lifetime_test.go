package ble

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanDuplicateAdvertisementsAndCancellationStopOnlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stops atomic.Int32
	err := runScan(ctx, func(stop func() bool) error {
		var group sync.WaitGroup
		for i := 0; i < 20; i++ {
			group.Add(1)
			go func() { defer group.Done(); stop() }()
		}
		cancel()
		group.Wait()
		return nil
	}, func() error { stops.Add(1); return nil })
	if err != nil || stops.Load() != 1 {
		t.Fatalf("scan stopped %d times: %v", stops.Load(), err)
	}
}
func TestScanCancellationRetriesOnlyUntilNativeStopIsAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	stopped := make(chan struct{})
	err := runScan(ctx, func(_ func() bool) error {
		select {
		case <-stopped:
			return nil
		case <-time.After(time.Second):
			return errors.New("scan did not stop")
		}
	}, func() error {
		if calls.Add(1) == 1 {
			return errors.New("native scan still starting")
		}
		close(stopped)
		return nil
	})
	if err != nil || calls.Load() != 2 {
		t.Fatalf("stop calls %d: %v", calls.Load(), err)
	}
}
