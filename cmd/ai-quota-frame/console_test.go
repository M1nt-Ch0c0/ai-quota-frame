package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

func TestConsoleQueuesOneOperationAndBlocksUncertainSwitch(t *testing.T) {
	s := console.NewState(time.Minute, "mint-skin", "ble", "id", t.TempDir())
	s.DeviceResult(json.RawMessage(`{"apps":[{"id":"color-test","abi":2,"button_eligible":true}]}`), true)
	paused := false
	b := newConsoleBridge(s, "id", "test-token", func(p bool) { paused = p }, func(context.Context) error { return nil })
	if r := b.handle(console.Request{Action: "pause"}); r.Error != "" || !paused || r.State.Automatic {
		t.Fatal("pause not applied")
	}
	b.handle(console.Request{Action: "resume"})
	if paused {
		t.Fatal("resume not applied")
	}
	s.Unknown()
	if r := b.handle(console.Request{Action: "switch", App: "color-test"}); r.Error == "" {
		t.Fatal("uncertain display allowed switch")
	}
	if len(b.jobs) != 0 {
		t.Fatal("blocked mutation was enqueued")
	}
	if r := b.handle(console.Request{Action: "status"}); r.Error != "" {
		t.Fatal("safe observation blocked")
	}
	if r := b.handle(console.Request{Action: "refresh"}); r.Error == "" {
		t.Fatal("duplicate operation accepted")
	}
	if r := b.handle(console.Request{Action: "delete"}); r.Error == "" {
		t.Fatal("arbitrary command accepted")
	}
}

func TestConsolePackageReviewBindsExactBytes(t *testing.T) {
	p := make([]byte, 4096+52)
	binary.LittleEndian.PutUint32(p, 0x31464c45)
	binary.LittleEndian.PutUint32(p[4:], 2)
	binary.LittleEndian.PutUint32(p[8:], 2)
	binary.LittleEndian.PutUint32(p[12:], 52)
	binary.LittleEndian.PutUint32(p[16:], 19)
	copy(p[52:], "photoframe")
	copy(p[4096:], "\x7fELF\x01\x01\x01")
	binary.LittleEndian.PutUint16(p[4096+16:], 3)
	binary.LittleEndian.PutUint16(p[4096+18:], 94)
	digest := sha256.Sum256(p[4096:])
	copy(p[20:], digest[:])
	path := filepath.Join(t.TempDir(), "app.pkg")
	if err := os.WriteFile(path, p, 0600); err != nil {
		t.Fatal(err)
	}
	s := console.NewState(time.Minute, "", "", "", t.TempDir())
	b := newConsoleBridge(s, "", "", func(bool) {}, nil)
	r := b.handle(console.Request{Action: "inspect", Path: path})
	if r.Error != "" || r.Package == nil || r.Package.Version != 19 {
		t.Fatal("package review failed", r.Error)
	}
	if r = b.handle(console.Request{Action: "install", Path: path, Digest: "wrong"}); r.Error == "" || len(b.jobs) != 0 {
		t.Fatal("unreviewed bytes enqueued")
	}
	_, metadata, err := readConsolePackage(path)
	if err != nil {
		t.Fatal(err)
	}
	if r = b.handle(console.Request{Action: "install", Path: path, Digest: metadata.Digest}); r.Error != "" || len(b.jobs) != 1 {
		t.Fatal("reviewed package not queued", r.Error)
	}
}

func TestConsoleGateDoesNotInterruptTransfer(t *testing.T) {
	b := newConsoleBridge(console.NewState(time.Minute, "", "", "", t.TempDir()), "", "", func(bool) {}, nil)
	if err := b.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.acquire(ctx); err == nil {
		t.Fatal("parallel device operation admitted")
	}
	b.release()
	if err := b.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.release()
}
