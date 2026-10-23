package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCompletionEvidenceAndSnapshotIsolation(t *testing.T) {
	s := NewState(15*time.Minute, "mint-skin", "ble", "9c139eb063e8", t.TempDir())
	s.Progress(42, 42)
	if v := s.Snapshot(); v.Transfer.CompletedAt != nil || v.PreviewPath != "" || v.Transfer.Stage != "displaying" {
		t.Fatal("upload counted as physical completion")
	}
	s.Pending()
	s.Stage("failed")
	if v := s.Snapshot(); !v.Transfer.Uncertain || v.Transfer.Stage != "recovering" {
		t.Fatal("receipt uncertainty was hidden by a retry")
	}
	s.Complete([]byte("test image"))
	v := s.Snapshot()
	if v.Transfer.CompletedAt == nil || v.Transfer.Uncertain {
		t.Fatal("completion not recorded")
	}
	if info, err := os.Stat(v.PreviewPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private completion artifact missing", err)
	}
	v.Events[0].Message = "mutated"
	if s.Snapshot().Events[0].Message == "mutated" {
		t.Fatal("snapshot aliases shared state")
	}
}

func TestDeviceObservationWhitelistAndReboot(t *testing.T) {
	s := NewState(time.Minute, "mint-skin", "ble", "id", t.TempDir())
	if err := s.DeviceResult(json.RawMessage(`{"uptime_ms":9000,"apps":[],"secret":"must not cross bridge"}`), true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeviceResult(json.RawMessage(`{"uptime_ms":1000,"apps":[]}`), true); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(s.Snapshot())
	var keys map[string]any
	_ = json.Unmarshal(p, &keys)
	if _, exists := keys["device"].(map[string]any)["secret"]; exists {
		t.Fatal("unknown device fields exposed")
	}
	count := 0
	for _, e := range s.Snapshot().Events {
		if e.Kind == "reset" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("reboot not recorded")
	}
	s.DeviceResult(nil, false)
	if s.Snapshot().Device.Uptime != 1000 || s.Snapshot().DeviceError == "" {
		t.Fatal("failed read destroyed historical observation")
	}
}

func TestLocalSocketPermissionsRoundTripAndShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket endpoint is not enabled on Windows")
	}
	dir, err := os.MkdirTemp("", "pp-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "private", "bridge.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, path, func(r Request) Response { return Response{Error: r.Action} }) }()
	var response Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err = Call(ctx, path, Request{Action: "snapshot"})
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || response.Error != "snapshot" {
		t.Fatal("local IPC failed", err)
	}
	for _, p := range []string{filepath.Dir(path), path} {
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("endpoint accessible to other users", e)
		}
	}
	if err = Serve(ctx, path, func(Request) Response { return Response{} }); err == nil {
		t.Fatal("second daemon replaced endpoint")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
}

func TestEndpointNeverReplacesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.sock")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), path, func(Request) Response { return Response{} }); err == nil {
		t.Fatal("replaced regular file")
	}
	p, _ := os.ReadFile(path)
	if string(p) != "keep" {
		t.Fatal("file was changed")
	}
}

func TestPausePreferenceSurvivesServiceRestart(t *testing.T) {
	dir := t.TempDir()
	s := NewState(time.Minute, "", "", "", dir)
	if err := s.Automatic(false); err != nil {
		t.Fatal(err)
	}
	if NewState(time.Minute, "", "", "", dir).Snapshot().Automatic {
		t.Fatal("restart silently resumed a paused sender")
	}
	if err := s.Automatic(true); err != nil {
		t.Fatal(err)
	}
	if !NewState(time.Minute, "", "", "", dir).Snapshot().Automatic {
		t.Fatal("resume preference lost")
	}
}
