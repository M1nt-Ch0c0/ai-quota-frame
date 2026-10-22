package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryAfterFailureChangesOnlyNextAttempt(t *testing.T) {
	var received, scans atomic.Int32
	fresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer unit-test-token" {
			t.Error("token missing")
		}
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer fresh.Close()
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer old.Close()
	p := New(old.URL+"/api/push", "unit-test-token", nil, nil)
	p.SetDiscovery(func(context.Context) (string, error) { scans.Add(1); return fresh.URL + "/api/push", nil })
	if err := p.post(context.Background(), []byte("frame")); err == nil {
		t.Fatal("lost response accepted")
	}
	if received.Load() != 0 || scans.Load() != 1 || p.endpoint != fresh.URL+"/api/push" {
		t.Fatal("discovery repeated physical operation or did not update target")
	}
	if err := p.post(context.Background(), []byte("frame")); err != nil || received.Load() != 1 {
		t.Fatalf("next scheduled attempt: %v", err)
	}
}
func TestDiscoveryThrottleFailureAndCancellation(t *testing.T) {
	p := New("http://192.0.2.1/api/push", "unit-test-token", nil, nil)
	scans := 0
	p.SetDiscovery(func(ctx context.Context) (string, error) {
		scans++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded discovery")
		}
		return "", errors.New("radio unavailable")
	})
	p.refreshEndpoint(context.Background())
	p.refreshEndpoint(context.Background())
	if scans != 1 || p.endpoint != "http://192.0.2.1/api/push" {
		t.Fatal("discovery tight loop or lost fallback")
	}
	p.lastDiscovery = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.refreshEndpoint(ctx)
	if scans != 1 {
		t.Fatal("cancelled discovery started")
	}
}
func TestDiscoveryRunsBeforeFirstPushWorkerWake(t *testing.T) {
	p := New("", "unit-test-token", nil, nil)
	called := make(chan struct{}, 1)
	p.SetDiscovery(func(context.Context) (string, error) { called <- struct{}{}; return "http://192.168.1.7/api/push", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("no startup discovery")
	}
	cancel()
	<-done
	if p.endpoint != "http://192.168.1.7/api/push" {
		t.Fatal("startup target not adopted")
	}
}
