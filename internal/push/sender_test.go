package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type permanentTransportError struct{}

func (permanentTransportError) Error() string   { return "image completion ambiguous" }
func (permanentTransportError) Permanent() bool { return true }

func TestSenderUsesNoHTTPAndRecordsOnlyPhysicalCompletion(t *testing.T) {
	var httpCalls, discoveryCalls, sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { httpCalls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) { return []byte("frame"), nil }))
	p := New(server.URL+"/api/push", "token", producer, nil)
	p.SetDiscovery(func(context.Context) (string, error) { discoveryCalls.Add(1); return server.URL + "/api/push", nil })
	entered := make(chan struct{})
	completed := make(chan struct{})
	p.SetSender(func(ctx context.Context, data []byte) error {
		if string(data) != "frame" {
			return errors.New("bad frame")
		}
		sends.Add(1)
		close(entered)
		select {
		case <-completed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	last := filepath.Join(t.TempDir(), "completed.png")
	p.SetLastFramePath(last)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	p.Enqueue(quota.Snapshot{})
	<-entered
	if _, err := os.Stat(last); !os.IsNotExist(err) {
		t.Fatal("upload acceptance counted as display completion")
	}
	close(completed)
	waitForIdle(t, p)
	got, err := os.ReadFile(last)
	if err != nil || string(got) != "frame" {
		t.Fatal("completed receipt not recorded", err)
	}
	p.Enqueue(quota.Snapshot{})
	waitForIdle(t, p)
	stopPusher(t, cancel, done)
	if sends.Load() != 1 || httpCalls.Load() != 0 || discoveryCalls.Load() != 0 {
		t.Fatal("BLE transport fell back to HTTP or repeated a completed image")
	}
}

func TestPermanentSenderFailureDoesNotAutomaticallyRepeatSameFrame(t *testing.T) {
	var sends atomic.Int32
	p := New("", "token", frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) { return []byte("frame"), nil })), nil)
	p.retryDelay = time.Millisecond
	p.SetSender(func(context.Context, []byte) error { sends.Add(1); return permanentTransportError{} })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	p.Enqueue(quota.Snapshot{})
	waitForIdle(t, p)
	p.Enqueue(quota.Snapshot{})
	waitForIdle(t, p)
	stopPusher(t, cancel, done)
	if sends.Load() != 1 {
		t.Fatal("ambiguous image automatically repeated")
	}
}
