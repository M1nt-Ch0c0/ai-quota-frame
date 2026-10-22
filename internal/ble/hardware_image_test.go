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

type lostImageReply struct {
	Link
	drop             *atomic.Bool
	commits, queries *atomic.Int32
}

func (l *lostImageReply) preferThroughput() {
	if link, ok := l.Link.(interface{ preferThroughput() }); ok {
		link.preferThroughput()
	}
}
func (l *lostImageReply) Exchange(ctx context.Context, request []byte) ([]byte, error) {
	if request[0] == OpCommit {
		l.commits.Add(1)
	}
	if request[0] == OpImageResult {
		l.queries.Add(1)
	}
	response, err := l.Link.Exchange(ctx, request)
	if err == nil && request[0] == OpCommit && len(response) >= 39 && binary.LittleEndian.Uint16(response[5:7]) == 0 && l.drop.CompareAndSwap(true, false) {
		return nil, errors.New("test discarded a physical-completion reply")
	}
	return response, err
}

func hwImageBegin(ctx context.Context, c *Client, data []byte, total uint32) ([]byte, error) {
	caps, err := c.command(ctx, OpImageCaps, nil)
	if err != nil {
		return nil, err
	}
	if len(caps) != 22 || caps[0] != 1 {
		return nil, errors.New("image capabilities unavailable")
	}
	p := make([]byte, 100)
	copy(p, caps[1:17])
	if _, err = rand.Read(p[16:32]); err != nil {
		return nil, err
	}
	binary.LittleEndian.PutUint32(p[32:], total)
	digest := sha256.Sum256(data)
	copy(p[36:], digest[:])
	copy(p[68:], "photoframe")
	_, err = c.command(ctx, OpImageBegin, p)
	return p, err
}

// Exactly one physical refresh. The application and its slots are not switched
// or updated. Permission to draw and human visual confirmation are required.
func TestHardwareOfflineBLEImage(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "image-offline" {
		t.Skip("explicit image-offline opt-in required")
	}
	data, err := os.ReadFile(os.Getenv("PHOTOFRAME_HARDWARE_PNG"))
	if err != nil {
		t.Fatal("read explicit PNG fixture")
	}
	if err = ValidatePNG(data); err != nil {
		t.Fatal(err)
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var before hardwareState
	offline := func(c *Client) error {
		if c.Info().State == 2 || !c.Info().IP.IsUnspecified() {
			return errors.New("device must have no Wi-Fi IPv4 for this test")
		}
		return nil
	}
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := offline(c); err != nil {
			return err
		}
		before, err = hwStatus(ctx, c)
		if err != nil {
			return err
		}
		found := false
		for _, app := range before.Apps {
			if app.ID == "photoframe" && app.Bank == before.Loaded {
				found = true
			}
		}
		if !found {
			return errors.New("original photoframe must be running before the image test")
		}
		hwMemory(t, "offline-before-image", before)
		if _, err = hwImageBegin(ctx, c, data, MaxImageSize); err != nil {
			return err
		}
		chunk := make([]byte, 4)
		chunk = append(chunk, data[:min(400, len(data))]...)
		if _, err = c.command(ctx, OpChunk, chunk); err != nil {
			return err
		}
		receiving, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "partial-5m-receive", receiving)
		if receiving.FreePSRAM+MaxImageSize-65536 > before.FreePSRAM {
			return errors.New("receive buffer was not allocated in PSRAM")
		}
		t.Log("disconnecting partial image before COMMIT; no draw was requested")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := offline(c); err != nil {
			return err
		}
		after, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "after-partial-disconnect", after)
		if after.FreePSRAM+65536 < before.FreePSRAM || after.Loaded != before.Loaded {
			return errors.New("partial receive did not release memory or preserve the app")
		}
		bad := []byte("not png!")
		if _, err = hwImageBegin(ctx, c, bad, uint32(len(bad))); err != nil {
			return err
		}
		chunk := append(make([]byte, 4), bad...)
		if _, err = c.command(ctx, OpChunk, chunk); err != nil {
			return err
		}
		_, err = c.command(ctx, OpCommit, nil)
		if err = hwExpect(err, 12); err != nil {
			return err
		}
		status, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		if status.RuntimeReady != before.RuntimeReady || status.Version != before.Version {
			return errors.New("invalid PNG changed application readiness/version")
		}
		t.Log("invalid PNG rejected through the shared decoder; receive admission reusable")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := NewImageSender(id, token, "photoframe")
	if err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	drop.Store(true)
	var commits, queries atomic.Int32
	sender.withClient = func(ctx context.Context, id, token string, job func(context.Context, *Client) error) error {
		return WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
			if err := offline(c); err != nil {
				return err
			}
			c.link = &lostImageReply{Link: c.link, drop: &drop, commits: &commits, queries: &queries}
			return job(ctx, c)
		})
	}
	started := time.Now()
	last := -1
	err = sender.Send(ctx, data, func(done, total int) {
		percent := done * 100 / total
		if percent/10 != last {
			last = percent / 10
			t.Logf("IMAGE %d%% (%d/%d) elapsed=%.1fs", percent, done, total, time.Since(started).Seconds())
		}
	})
	if !errors.Is(err, ErrImagePending) {
		t.Fatalf("expected discarded completion reply, got %v", err)
	}
	t.Log("physical completion reply deliberately discarded; reconnecting to query the same receipt")
	if err = sender.Send(ctx, data, nil); err != nil {
		t.Fatal(err)
	}
	if commits.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("unexpected COMMIT/query counts: %d/%d", commits.Load(), queries.Load())
	}
	t.Logf("authenticated physical completion recovered in %.1fs; exactly one COMMIT", time.Since(started).Seconds())
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		if err := offline(c); err != nil {
			return err
		}
		after, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "offline-after-image", after)
		if after.Loaded != before.Loaded || after.Version != before.Version || !after.RuntimeReady {
			return errors.New("image did not confirm the original application")
		}
		if after.UptimeMS < before.UptimeMS || after.ResetReason != before.ResetReason {
			return errors.New("device rebooted during BLE image test")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
