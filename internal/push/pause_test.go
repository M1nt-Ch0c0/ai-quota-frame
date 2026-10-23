package push

import (
	"context"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestPauseKeepsLatestAndDoesNotCancelAdmittedDisplay(t *testing.T) {
	producer := frame.NewProducer(rendererFunc(func(s quota.Snapshot) ([]byte, error) { return []byte(s.Accounts[0].Name), nil }))
	p := New("", "", producer, nil)
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	canceled := make(chan struct{}, 1)
	p.SetSender(func(ctx context.Context, _ []byte) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			canceled <- struct{}{}
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	defer stopPusher(t, cancel, done)
	p.SetPaused(true)
	p.Enqueue(quota.Snapshot{DataUpdatedAt: time.Unix(1, 0), Accounts: []quota.Account{{Name: "first"}}})
	select {
	case <-entered:
		t.Fatal("paused sender ran")
	case <-time.After(30 * time.Millisecond):
	}
	p.SetPaused(false)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resume failed")
	}
	p.SetPaused(true)
	select {
	case <-canceled:
		t.Fatal("pause canceled an admitted display")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	waitForIdle(t, p)
	p.Enqueue(quota.Snapshot{DataUpdatedAt: time.Unix(2, 0), Accounts: []quota.Account{{Name: "second"}}})
	select {
	case <-entered:
		t.Fatal("sent another frame while paused")
	case <-time.After(30 * time.Millisecond):
	}
	p.SetPaused(false)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("latest queued frame lost")
	}
	waitForIdle(t, p)
}
