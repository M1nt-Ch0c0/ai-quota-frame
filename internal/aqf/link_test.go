package aqf

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

type fakeDevice struct {
	pings    atomic.Int32
	shellErr error
}

func (d *fakeDevice) Info() ble.DeviceInfo { return ble.DeviceInfo{Version: 2, ID: "000102030405"} }
func (d *fakeDevice) Ping(context.Context) error {
	d.pings.Add(1)
	return nil
}
func (d *fakeDevice) Status(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"apps":[]}`), nil
}
func (d *fakeDevice) Shell(_ context.Context, line string) (ble.ShellResult, error) {
	if d.shellErr != nil {
		return ble.ShellResult{}, d.shellErr
	}
	return ble.ShellResult{Output: []byte("ran " + line + "\n")}, nil
}

// fakeDialer counts connections and holds each one open until its job returns.
func fakeDialer(d *fakeDevice, dials *atomic.Int32) Dialer {
	return func(ctx context.Context, job func(context.Context, Device) error) error {
		dials.Add(1)
		return job(ctx, d)
	}
}

func startLink(t *testing.T, d *fakeDevice, keepalive, idle time.Duration) (*Link, *atomic.Int32) {
	t.Helper()
	var dials atomic.Int32
	l := NewLink("000102030405", fakeDialer(d, &dials), keepalive, idle)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return l, &dials
}

func shell(t *testing.T, l *Link, line string) (ble.ShellResult, error) {
	t.Helper()
	var result ble.ShellResult
	err := l.Do(context.Background(), func(ctx context.Context, d Device) error {
		var e error
		result, e = d.Shell(ctx, line)
		return e
	})
	return result, err
}

func TestLinkReusesOneConnectionAndKeepsAlive(t *testing.T) {
	d := &fakeDevice{}
	l, dials := startLink(t, d, 5*time.Millisecond, 0)
	for _, line := range []string{"uname", "apps", "free"} {
		if r, err := shell(t, l, line); err != nil || string(r.Output) != "ran "+line+"\n" {
			t.Fatalf("%s: %q %v", line, r.Output, err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	if dials.Load() != 1 || d.pings.Load() == 0 || !l.State().Connected {
		t.Fatalf("dials=%d pings=%d state=%+v", dials.Load(), d.pings.Load(), l.State())
	}
}

func TestLinkReconnectsOnlyAfterTransportFailure(t *testing.T) {
	d := &fakeDevice{shellErr: ble.ErrShellUnsupported}
	l, dials := startLink(t, d, time.Hour, 0)
	if _, err := shell(t, l, "uname"); !errors.Is(err, ble.ErrShellUnsupported) {
		t.Fatal(err)
	}
	d.shellErr = ble.DeviceError(3)
	if _, err := shell(t, l, "uname"); err == nil || dials.Load() != 1 {
		t.Fatalf("device rejection dropped the session: dials=%d", dials.Load())
	}
	d.shellErr = errors.New("link lost")
	if _, err := shell(t, l, "uname"); err == nil {
		t.Fatal("expected link failure")
	}
	d.shellErr = nil
	if _, err := shell(t, l, "uname"); err != nil || dials.Load() != 2 {
		t.Fatalf("reconnect: dials=%d err=%v", dials.Load(), err)
	}
	if l.State().Connections != 2 {
		t.Fatalf("state = %+v", l.State())
	}
}

func TestLinkIdleAndExplicitDisconnect(t *testing.T) {
	d := &fakeDevice{}
	l, dials := startLink(t, d, time.Hour, 10*time.Millisecond)
	if _, err := shell(t, l, "uptime"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for l.State().Connected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if l.State().Connected {
		t.Fatal("idle session stayed connected")
	}
	if _, err := shell(t, l, "uptime"); err != nil || dials.Load() != 2 {
		t.Fatalf("dials=%d err=%v", dials.Load(), err)
	}
}

func TestLinkDialFailureAnswersFirstCall(t *testing.T) {
	l := NewLink("000102030405", func(context.Context, func(context.Context, Device) error) error {
		return errors.New("BLE device not found")
	}, time.Hour, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)
	err := l.Do(ctx, func(context.Context, Device) error { return nil })
	if err == nil || err.Error() != "BLE device not found" || l.State().LastError == "" {
		t.Fatalf("err=%v state=%+v", err, l.State())
	}
}

func TestServerOverSocket(t *testing.T) {
	d := &fakeDevice{}
	l, dials := startLink(t, d, time.Hour, 0)
	path := filepath.Join(t.TempDir(), "aqf.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	server := &Server{Link: l, Version: "test", Stop: func() { close(stopped); cancel() }}
	go server.Serve(ctx, listener)
	if _, err := Listen(path); err == nil {
		t.Fatal("second server replaced the live socket")
	}
	conn, err := Dial(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var result ble.ShellResult
	for _, line := range []string{"uname", "apps"} {
		if err := conn.Call(Request{Method: "shell", Command: line}, &result); err != nil || string(result.Output) != "ran "+line+"\n" {
			t.Fatalf("%q %v", result.Output, err)
		}
	}
	if err := conn.Call(Request{Method: "shell", Command: "a\nb"}, &result); err == nil {
		t.Fatal("control characters accepted")
	}
	if err := conn.Call(Request{Method: "status", DeviceID: "aa:bb:cc:dd:ee:ff"}, nil); err == nil {
		t.Fatal("request for another device accepted")
	}
	var status json.RawMessage
	if err := conn.Call(Request{Method: "status", DeviceID: "00:01:02:03:04:05"}, &status); err != nil || string(status) != `{"apps":[]}` {
		t.Fatalf("%s %v", status, err)
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d", dials.Load())
	}
	if err := conn.Call(Request{Method: "kill"}, nil); err != nil {
		t.Fatal(err)
	}
	<-stopped
}

func TestExclusiveClosesSessionAndBlocksReconnect(t *testing.T) {
	d := &fakeDevice{}
	l, dials := startLink(t, d, time.Hour, 0)
	if _, err := shell(t, l, "uname"); err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	err := l.Exclusive(context.Background(), func(context.Context) error {
		if l.State().Connected {
			return errors.New("session still open during exclusive work")
		}
		go func() { _, err := shell(t, l, "apps"); queued <- err }()
		time.Sleep(20 * time.Millisecond)
		if dials.Load() != 1 {
			return errors.New("reconnected during exclusive work")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-queued; err != nil || dials.Load() != 2 {
		t.Fatalf("queued call: dials=%d err=%v", dials.Load(), err)
	}
	if err := l.Exclusive(context.Background(), func(context.Context) error { return nil }); err != nil || dials.Load() != 2 {
		t.Fatalf("idle exclusive: dials=%d err=%v", dials.Load(), err)
	}
}
