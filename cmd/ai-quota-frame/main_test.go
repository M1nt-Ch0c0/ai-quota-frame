package main

import (
	"net"
	"testing"
)

func TestRunReturnsFailureWhenListenerCannotStart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test listener: %v", err)
	}
	defer listener.Close()

	t.Setenv("LISTEN_ADDR", listener.Addr().String())
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("FRAME_ACCESS_TOKEN", "test-frame-token")
	t.Setenv("TZ", "UTC")
	if code := run(); code != 1 {
		t.Fatalf("run() exit code = %d, want 1", code)
	}
}
