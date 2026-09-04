package frame

import (
	"bytes"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type countingRenderer struct {
	calls atomic.Int32
}

func (renderer *countingRenderer) Render(snapshot quota.Snapshot) ([]byte, error) {
	renderer.calls.Add(1)
	remaining := byte(0)
	source := byte(0)
	if len(snapshot.Accounts) > 0 && len(snapshot.Accounts[0].Windows) > 0 && snapshot.Accounts[0].Windows[0].RemainingPercent != nil {
		remaining = byte(*snapshot.Accounts[0].Windows[0].RemainingPercent)
		if snapshot.Accounts[0].Windows[0].Source == "demo" {
			source = 1
		}
	}
	return []byte{'p', 'n', 'g', remaining, source}, nil
}

func TestProducerCachesByVisibleSnapshotSemantics(t *testing.T) {
	renderer := &countingRenderer{}
	producer := NewProducer(renderer)
	now := time.Date(2026, time.September, 4, 8, 0, 0, 0, time.UTC)
	remaining := 80.0
	observedAt := now
	first := quota.Snapshot{
		DataUpdatedAt: now,
		RefreshedAt:   now,
		NextRefreshAt: now.Add(5 * time.Minute),
		Accounts: []quota.Account{{
			Provider: "codex",
			Name:     "masked",
			Status:   "ok",
			Windows: []quota.Window{{
				ID: "5h", RemainingPercent: &remaining, ObservedAt: &observedAt, Source: "active",
			}},
		}},
	}

	firstImage, err := producer.Produce(first)
	if err != nil {
		t.Fatalf("Produce(first) error = %v", err)
	}
	second := first
	second.RefreshedAt = now.Add(time.Minute)
	second.NextRefreshAt = now.Add(6 * time.Minute)
	newObservation := observedAt.Add(time.Minute)
	second.Accounts = append([]quota.Account(nil), first.Accounts...)
	second.Accounts[0].Name = "different masked name"
	second.Accounts[0].Windows = append([]quota.Window(nil), first.Accounts[0].Windows...)
	second.Accounts[0].Windows[0].ObservedAt = &newObservation
	second.Accounts[0].Windows[0].Source = "passive"
	secondImage, err := producer.Produce(second)
	if err != nil {
		t.Fatalf("Produce(second) error = %v", err)
	}
	if renderer.calls.Load() != 1 {
		t.Fatalf("Render() calls = %d, want 1", renderer.calls.Load())
	}
	if !bytes.Equal(firstImage.Payload, secondImage.Payload) || firstImage.Digest != secondImage.Digest {
		t.Fatal("semantically identical snapshots did not reuse the cached image")
	}

	changedRemaining := 70.0
	second.Accounts[0].Windows[0].RemainingPercent = &changedRemaining
	second.DataUpdatedAt = now.Add(2 * time.Minute)
	changedImage, err := producer.Produce(second)
	if err != nil {
		t.Fatalf("Produce(changed) error = %v", err)
	}
	if renderer.calls.Load() != 2 {
		t.Fatalf("Render() calls after visible change = %d, want 2", renderer.calls.Load())
	}
	if bytes.Equal(firstImage.Payload, changedImage.Payload) || firstImage.Digest == changedImage.Digest {
		t.Fatal("visible snapshot change reused stale image")
	}
	digest := sha256.Sum256(changedImage.Payload)
	if changedImage.Digest != digest {
		t.Fatalf("image digest = %x, want %x", changedImage.Digest, digest)
	}

	demo := second
	demo.Accounts = append([]quota.Account(nil), second.Accounts...)
	demo.Accounts[0].Windows = append([]quota.Window(nil), second.Accounts[0].Windows...)
	demo.Accounts[0].Windows[0].Source = "demo"
	demoImage, err := producer.Produce(demo)
	if err != nil {
		t.Fatalf("Produce(demo source) error = %v", err)
	}
	if renderer.calls.Load() != 3 {
		t.Fatalf("Render() calls after visible source change = %d, want 3", renderer.calls.Load())
	}
	if bytes.Equal(changedImage.Payload, demoImage.Payload) || changedImage.Digest == demoImage.Digest {
		t.Fatal("DEMO/LIVE source change reused a stale cached image")
	}
}

func TestProducerSerializesConcurrentRendering(t *testing.T) {
	renderer := &countingRenderer{}
	producer := NewProducer(renderer)
	snapshot := quota.Snapshot{DataUpdatedAt: time.Now().UTC(), Accounts: []quota.Account{}}

	const workers = 8
	var group sync.WaitGroup
	errors := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := producer.Produce(snapshot)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Produce() error = %v", err)
		}
	}
	if renderer.calls.Load() != 1 {
		t.Fatalf("concurrent Render() calls = %d, want 1", renderer.calls.Load())
	}
}
