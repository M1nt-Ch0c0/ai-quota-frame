package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/aqf"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

type testDevice struct{}

func (testDevice) Info() ble.DeviceInfo       { return ble.DeviceInfo{Version: 2, ID: "000102030405"} }
func (testDevice) Ping(context.Context) error { return nil }
func (testDevice) Status(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"apps":[]}`), nil
}
func (testDevice) Shell(_ context.Context, line string) (ble.ShellResult, error) {
	if line == "nope" {
		return ble.ShellResult{Exit: 127, Output: []byte("nope: command not found")}, nil
	}
	return ble.ShellResult{Output: []byte("> " + line + "\n")}, nil
}

func startTestServer(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	link := aqf.NewLink("000102030405", func(ctx context.Context, job func(context.Context, aqf.Device) error) error {
		return job(ctx, testDevice{})
	}, time.Hour, 0)
	path, err := aqf.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := aqf.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go link.Run(ctx)
	go (&aqf.Server{Link: link, Version: "test", Stop: cancel}).Serve(ctx, listener)
}

func TestShellCommandsThroughServer(t *testing.T) {
	startTestServer(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"shell", "uname", "-a"}, nil, &stdout, &stderr); code != 0 || stdout.String() != "> uname -a\n" {
		t.Fatalf("code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"-s", "000102030405", "shell", "nope"}, nil, &stdout, &stderr); code != 127 || stdout.String() != "nope: command not found\n" {
		t.Fatalf("code=%d out=%q", code, stdout.String())
	}
	stdout.Reset()
	in := strings.NewReader("apps\n\nfree\nexit\nuptime\n")
	if code := run([]string{"shell"}, in, &stdout, &stderr); code != 0 || stdout.String() != "> apps\n> free\n" {
		t.Fatalf("interactive code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"devices"}, nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "000102030405\tdevice") {
		t.Fatalf("devices: %q", stdout.String())
	}
	if code := run([]string{"-s", "aabbccddeeff", "status"}, nil, &stdout, &stderr); code != 1 {
		t.Fatal("status for a different device succeeded")
	}
}
