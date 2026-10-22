//go:build photopainter_hardware && (windows || linux || (darwin && cgo))

package ble

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func hwAppOffline(c *Client) error {
	if c.Info().State == 2 || !c.Info().IP.IsUnspecified() {
		return errors.New("test requires no device Wi-Fi IPv4")
	}
	return nil
}
func hwAppBegin(ctx context.Context, c *Client, target []byte, data []byte, total uint32) ([16]byte, appImageReceipt, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return id, appImageReceipt{}, err
	}
	p := make([]byte, 36)
	binary.LittleEndian.PutUint32(p, total)
	digest := sha256.Sum256(data)
	copy(p[4:], digest[:])
	r, err := c.appImageExchange(ctx, target, appImageWire(appImageBegin, id, p))
	return id, r, err
}

// Reserves app-owned RAM, disconnects, rejects bad input and cancels reception.
// No valid image is committed and no display is requested.
func TestHardwareAppImageSafety(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "app-image-safety" {
		t.Skip("explicit app-image-safety opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	var before hardwareState
	err := WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := hwAppOffline(c); err != nil {
			return err
		}
		var err error
		before, err = hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-before-receive", before)
		target, err := c.imageTarget(ctx, "photoframe")
		if err != nil {
			return err
		}
		request, r, err := hwAppBegin(ctx, c, target, []byte("partial!"), MaxImageSize)
		if err != nil {
			return err
		}
		if r.state != appImageReceiving {
			return errors.New("app refused maximum reservation")
		}
		tail := make([]byte, 4)
		tail = append(tail, []byte("partial!")...)
		if _, err = c.appImageExchange(ctx, target, appImageWire(appImageChunk, request, tail)); err != nil {
			return err
		}
		receiving, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-partial-5m", receiving)
		if receiving.FreePSRAM+MaxImageSize-65536 > before.FreePSRAM {
			return errors.New("app receive buffer was not placed in PSRAM")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := hwAppOffline(c); err != nil {
			return err
		}
		target, err := c.imageTarget(ctx, "photoframe")
		if err != nil {
			return err
		}
		// Flush the new CONNECTED boundary before sampling cleanup.
		var absent [16]byte
		absent[0] = 1
		if _, err = c.appImageExchange(ctx, target, appImageWire(appImageQuery, absent, nil)); err != nil {
			return err
		}
		after, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-after-disconnect", after)
		if after.FreePSRAM+65536 < before.FreePSRAM || after.UptimeMS < before.UptimeMS || after.ResetReason != before.ResetReason {
			return errors.New("app cleanup failed or device rebooted")
		}
		bad := []byte("not png!")
		request, r, err := hwAppBegin(ctx, c, target, bad, uint32(len(bad)))
		if err != nil {
			return err
		}
		if r.state != appImageReceiving {
			return errors.New("bad PNG begin failed")
		}
		tail := append(make([]byte, 4), bad...)
		if _, err = c.appImageExchange(ctx, target, appImageWire(appImageChunk, request, tail)); err != nil {
			return err
		}
		r, err = c.appImageExchange(ctx, target, appImageWire(appImageCommit, request, nil))
		if err != nil {
			return err
		}
		if r.state != appImageFailed || r.code != 7 {
			return errors.New("bad PNG was not rejected by app decoder")
		}
		t.Log("app decoder rejected invalid PNG before display")
		request, r, err = hwAppBegin(ctx, c, target, bad, MaxImageSize)
		if err != nil {
			return err
		}
		if r.state != appImageReceiving {
			return errors.New("reservation after reject failed")
		}
		r, err = c.appImageExchange(ctx, target, appImageWire(appImageCancel, request, nil))
		if err != nil {
			return err
		}
		if r.state != appImageCanceled {
			return errors.New("cancel not acknowledged")
		}
		after, err = hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-after-cancel", after)
		if after.FreePSRAM+65536 < before.FreePSRAM || after.Version != before.Version || !after.RuntimeReady {
			return errors.New("cancel leaked memory or invalidated app")
		}
		request, r, err = hwAppBegin(ctx, c, target, bad, MaxImageSize)
		if err != nil {
			return err
		}
		if r.state != appImageReceiving {
			return errors.New("timeout reservation failed")
		}
		// Keep only the host session alive; no APP command extends this receive.
		for i := 0; i < 3; i++ {
			if err = wait(ctx, 22*time.Second); err != nil {
				return err
			}
			if _, err = c.command(ctx, OpInfo, nil); err != nil {
				return err
			}
			t.Logf("host session alive, APP receive idle %d seconds", (i+1)*22)
		}
		r, err = c.appImageExchange(ctx, target, appImageWire(appImageQuery, request, nil))
		if err != nil {
			return err
		}
		if r.state != appImageCanceled || r.code != 9 {
			return errors.New("app did not expire its idle receive")
		}
		after, err = hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-after-idle-timeout", after)
		if after.FreePSRAM+65536 < before.FreePSRAM || after.UptimeMS < before.UptimeMS {
			return errors.New("idle timeout leaked memory or reset device")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type lostAppImageReply struct {
	Link
	drop             *atomic.Bool
	commits, queries *atomic.Int32
}

func (l *lostAppImageReply) preferThroughput() {
	if p, ok := l.Link.(interface{ preferThroughput() }); ok {
		p.preferThroughput()
	}
}
func (l *lostAppImageReply) Exchange(ctx context.Context, p []byte) ([]byte, error) {
	command := byte(0)
	if len(p) >= 5+12+18+32 && p[0] == OpAppWrite {
		command = p[18]
	}
	if command == appImageCommit {
		l.commits.Add(1)
	}
	if command == appImageQuery {
		l.queries.Add(1)
	}
	r, err := l.Link.Exchange(ctx, p)
	if err == nil && command == appImageCommit && len(r) >= 39 && binary.LittleEndian.Uint16(r[5:7]) == 0 && l.drop.CompareAndSwap(true, false) {
		return nil, errors.New("test discarded app COMMIT reply")
	}
	return r, err
}

// Exactly one valid COMMIT; a lost response is recovered from the app receipt.
func TestHardwareAppImageDisplay(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "app-image-display" {
		t.Skip("explicit app-image-display opt-in required")
	}
	data, err := os.ReadFile(os.Getenv("PHOTOFRAME_HARDWARE_PNG"))
	if err != nil {
		t.Fatal("read explicit PNG fixture")
	}
	if err = ValidatePNG(data); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	s, err := NewAppImageSender(id, token, "photoframe")
	if err != nil {
		t.Fatal(err)
	}
	var before hardwareState
	var drop atomic.Bool
	drop.Store(true)
	var commits, queries atomic.Int32
	s.withClient = func(ctx context.Context, id, token string, job func(context.Context, *Client) error) error {
		return WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
			if err := hwAppOffline(c); err != nil {
				return err
			}
			if before.UptimeMS == 0 {
				var err error
				before, err = hwStatus(ctx, c)
				if err != nil {
					return err
				}
				hwMemory(t, "app-before-display", before)
			}
			c.link = &lostAppImageReply{c.link, &drop, &commits, &queries}
			return job(ctx, c)
		})
	}
	started := time.Now()
	last := -1
	err = s.Send(ctx, data, func(n, total int) {
		p := n * 100 / total
		if p/10 != last {
			last = p / 10
			t.Logf("APP IMAGE %d%% (%d/%d) elapsed=%.1fs", p, n, total, time.Since(started).Seconds())
		}
	})
	if !errors.Is(err, ErrImagePending) {
		t.Fatal("expected deliberately lost reply", err)
	}
	if err = s.Send(ctx, data, nil); err != nil {
		t.Fatal(err)
	}
	if commits.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("COMMIT/query counts %d/%d", commits.Load(), queries.Load())
	}
	t.Logf("authenticated APP receipt recovered in %.1fs; exactly one COMMIT, physical refresh and POWER_OFF complete", time.Since(started).Seconds())
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := hwAppOffline(c); err != nil {
			return err
		}
		after, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "app-after-display", after)
		if after.UptimeMS < before.UptimeMS || after.ResetReason != before.ResetReason || after.Version != before.Version || !after.RuntimeReady {
			return errors.New("device reset or app changed during display")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
