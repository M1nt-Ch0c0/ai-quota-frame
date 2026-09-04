package push

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
)

type rendererFunc func(quota.Snapshot) ([]byte, error)

func (function rendererFunc) Render(snapshot quota.Snapshot) ([]byte, error) {
	return function(snapshot)
}

type fetcherFunc func(context.Context) ([]quota.Account, []string, error)

func (function fetcherFunc) Fetch(ctx context.Context) ([]quota.Account, []string, error) {
	return function(ctx)
}

func TestSuccessfulServiceRefreshTriggersBackgroundPush(t *testing.T) {
	pushed := make(chan []byte, 2)
	device := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		pushed <- body
		response.WriteHeader(http.StatusOK)
	}))
	defer device.Close()

	remaining := 64.0
	quotaService := service.New(fetcherFunc(func(context.Context) ([]quota.Account, []string, error) {
		return []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok",
			Windows: []quota.Window{{ID: "5h", RemainingPercent: &remaining}},
		}}, nil, nil
	}), time.Minute)
	producer := frame.NewProducer(rendererFunc(func(snapshot quota.Snapshot) ([]byte, error) {
		return []byte(fmt.Sprintf("ready:%.0f", *snapshot.Accounts[0].Windows[0].RemainingPercent)), nil
	}))
	pusher := New(device.URL+"/api/push", "push-token", producer, nil)
	quotaService.SetRefreshObserver(pusher.ObserveRefresh)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	if err := quotaService.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	select {
	case body := <-pushed:
		if got, want := string(body), "ready:64"; got != want {
			t.Fatalf("pushed body = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("successful ready refresh did not trigger a push")
	}
	waitForIdle(t, pusher)
	if err := quotaService.Refresh(context.Background()); err != nil {
		t.Fatalf("unchanged Refresh() error = %v", err)
	}
	waitForIdle(t, pusher)
	select {
	case body := <-pushed:
		t.Fatalf("unchanged ready snapshot pushed again: %q", body)
	case <-time.After(75 * time.Millisecond):
	}
	stopPusher(t, cancel, done)
}

func TestFailedRefreshInvalidatesOldLiveRetryAndNextSuccessPushesLatest(t *testing.T) {
	requestBodies := make(chan string, 3)
	firstRequestCanceled := make(chan struct{})
	var requestCount atomic.Int32
	device := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requestBodies <- string(body)
		if requestCount.Add(1) == 1 {
			<-request.Context().Done()
			close(firstRequestCanceled)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer device.Close()

	upstreamErr := errors.New("quota refresh failed")
	refreshNumber := 0
	quotaService := service.New(fetcherFunc(func(context.Context) ([]quota.Account, []string, error) {
		refreshNumber++
		switch refreshNumber {
		case 1:
			return accountsWithRemaining(80), nil, nil
		case 2:
			return nil, nil, upstreamErr
		default:
			return accountsWithRemaining(60), nil, nil
		}
	}), time.Minute)
	producer := frame.NewProducer(rendererFunc(func(snapshot quota.Snapshot) ([]byte, error) {
		if snapshot.Stale {
			return []byte("STALE"), nil
		}
		return []byte(fmt.Sprintf("LIVE:%.0f", *snapshot.Accounts[0].Windows[0].RemainingPercent)), nil
	}))
	pusher := New(device.URL+"/api/push", "push-token", producer, nil)
	pusher.retryDelay = 20 * time.Millisecond
	quotaService.SetRefreshObserver(pusher.ObserveRefresh)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	if err := quotaService.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	select {
	case body := <-requestBodies:
		if body != "LIVE:80" {
			t.Fatalf("first push body = %q, want LIVE:80", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first LIVE push did not start")
	}
	if err := quotaService.Refresh(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("failed Refresh() error = %v, want %v", err, upstreamErr)
	}
	select {
	case <-firstRequestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("failed refresh did not cancel the obsolete LIVE request")
	}
	time.Sleep(4 * pusher.retryDelay)
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("obsolete LIVE frame was retried after failed refresh: requests = %d, want 1", got)
	}
	pusher.mu.Lock()
	pendingAfterFailure := pusher.pending != nil
	pusher.mu.Unlock()
	if pendingAfterFailure {
		t.Fatal("failed refresh left the previous LIVE frame pending")
	}

	if err := quotaService.Refresh(context.Background()); err != nil {
		t.Fatalf("recovery Refresh() error = %v", err)
	}
	select {
	case body := <-requestBodies:
		if body != "LIVE:60" {
			t.Fatalf("recovery push body = %q, want latest LIVE:60", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("latest successful refresh was not pushed")
	}
	waitForIdle(t, pusher)
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("device requests = %d, want failed LIVE:80 then successful LIVE:60", got)
	}
	stopPusher(t, cancel, done)
}

func TestPusherPostsRawPNGWithRequiredHeadersAndDeduplicatesDigest(t *testing.T) {
	payload := []byte("exact raw PNG bytes")
	requests := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/push" {
			t.Errorf("request = %s %s, want POST /api/push", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", got)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer dedicated-push-token" {
			t.Errorf("Authorization = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("ReadAll(request.Body) error = %v", err)
		}
		if request.ContentLength != int64(len(body)) {
			t.Errorf("Content-Length = %d, want %d", request.ContentLength, len(body))
		}
		requests <- body
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var renders atomic.Int32
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		renders.Add(1)
		return append([]byte(nil), payload...), nil
	}))
	pusher := New(server.URL+"/api/push", "dedicated-push-token", producer, nil)
	if pusher.client.Timeout != 11*time.Minute || pusher.client.Timeout <= 5*120*time.Second {
		t.Fatalf("push timeout = %v, want 11m and longer than five 120s BUSY waits", pusher.client.Timeout)
	}
	transport, ok := pusher.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("PhotoPainter push client must connect directly without an environment proxy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	first := quota.Snapshot{DataUpdatedAt: time.Unix(1, 0), Accounts: []quota.Account{}}
	pusher.Enqueue(first)
	select {
	case body := <-requests:
		if !bytes.Equal(body, payload) {
			t.Fatalf("push body = %q, want %q", body, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first push")
	}
	waitForIdle(t, pusher)

	second := first
	second.DataUpdatedAt = first.DataUpdatedAt.Add(time.Minute)
	second.Errors = []string{"visible semantic change that renders identically"}
	pusher.Enqueue(second)
	waitForIdle(t, pusher)
	select {
	case body := <-requests:
		t.Fatalf("identical PNG was pushed again: %q", body)
	case <-time.After(75 * time.Millisecond):
	}
	if renders.Load() != 2 {
		t.Fatalf("Render() calls = %d, want 2 semantic images before digest dedupe", renders.Load())
	}
	stopPusher(t, cancel, done)
}

func TestPusherDoesNotFollowRedirectOrForwardBearerToken(t *testing.T) {
	const secret = "redirect-sensitive-push-token"
	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("initial Authorization = %q", got)
		}
		http.Redirect(response, request, redirectTarget.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()

	pusher := New(redirectSource.URL+"/api/push", secret, frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		return []byte("png"), nil
	})), nil)
	err := pusher.post(context.Background(), []byte("png"))
	if err == nil {
		t.Fatal("redirect response was accepted as a successful push")
	}
	var permanent *permanentPushError
	if !errors.As(err, &permanent) {
		t.Fatalf("redirect error = %v, want permanent failure", err)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("redirect target received %d requests; Bearer token may have escaped", redirectedRequests.Load())
	}
}

func TestPusherRetriesFailureAndMarksDigestOnlyAfterHTTP200(t *testing.T) {
	const secret = "token-must-never-appear-in-logs"
	requests := make(chan int, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		current := int(count.Add(1))
		requests <- current
		if current == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		return []byte("retryable PNG"), nil
	}))
	pusher := New(server.URL+"/api/push", secret, producer, logger)
	pusher.retryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()
	pusher.Enqueue(quota.Snapshot{DataUpdatedAt: time.Unix(3, 0), Accounts: []quota.Account{}})

	for want := 1; want <= 2; want++ {
		select {
		case got := <-requests:
			if got != want {
				t.Fatalf("request sequence = %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for request %d", want)
		}
	}
	waitForIdle(t, pusher)
	pusher.mu.Lock()
	hasLast := pusher.hasLast
	wantDigest := sha256.Sum256([]byte("retryable PNG"))
	last := pusher.last
	pusher.mu.Unlock()
	if !hasLast || last != wantDigest {
		t.Fatalf("successful digest = %x/%v, want %x/true", last, hasLast, wantDigest)
	}
	stopPusher(t, cancel, done)
	if strings.Contains(logs.String(), secret) {
		t.Fatal("push logs exposed PHOTOFRAME_PUSH_TOKEN")
	}
}

func TestPusherLatestPendingSnapshotWinsWithoutConcurrentRequests(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	bodies := make(chan string, 3)
	var requests atomic.Int32
	var active atomic.Int32
	var maximumActive atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		currentActive := active.Add(1)
		for {
			maximum := maximumActive.Load()
			if currentActive <= maximum || maximumActive.CompareAndSwap(maximum, currentActive) {
				break
			}
		}
		defer active.Add(-1)
		body, _ := io.ReadAll(request.Body)
		requestNumber := requests.Add(1)
		bodies <- string(body)
		if requestNumber == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	producer := frame.NewProducer(rendererFunc(func(snapshot quota.Snapshot) ([]byte, error) {
		return []byte(snapshot.Errors[0]), nil
	}))
	pusher := New(server.URL+"/api/push", "push-token", producer, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()
	pusher.Enqueue(namedSnapshot("first"))
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first push did not start")
	}
	pusher.Enqueue(namedSnapshot("superseded"))
	pusher.Enqueue(namedSnapshot("latest"))
	close(releaseFirst)

	wantBodies := []string{"first", "latest"}
	for index, want := range wantBodies {
		select {
		case got := <-bodies:
			if got != want {
				t.Fatalf("push %d body = %q, want %q", index+1, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for push %d", index+1)
		}
	}
	waitForIdle(t, pusher)
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want first and latest only", requests.Load())
	}
	if maximumActive.Load() != 1 {
		t.Fatalf("maximum concurrent device requests = %d, want 1", maximumActive.Load())
	}
	stopPusher(t, cancel, done)
}

func TestPusherRecordsCompletedSupersededDigestWithoutRefreshingTwice(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests atomic.Int32
	device := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer device.Close()

	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		return []byte("same physical PNG"), nil
	}))
	pusher := New(device.URL+"/api/push", "push-token", producer, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	pusher.Enqueue(namedSnapshot("first semantics"))
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first push did not start")
	}
	pusher.Enqueue(namedSnapshot("new semantics, identical PNG"))
	close(releaseFirst)
	waitForIdle(t, pusher)
	if got := requests.Load(); got != 1 {
		t.Fatalf("identical replacement caused %d physical refreshes, want 1", got)
	}
	stopPusher(t, cancel, done)
}

func TestPusherDropsSupersededFrameBeforeStartingRequest(t *testing.T) {
	renderStarted := make(chan struct{})
	releaseRender := make(chan struct{})
	var renderCount atomic.Int32
	producer := frame.NewProducer(rendererFunc(func(snapshot quota.Snapshot) ([]byte, error) {
		if renderCount.Add(1) == 1 {
			close(renderStarted)
			<-releaseRender
		}
		return []byte(snapshot.Errors[0]), nil
	}))
	bodies := make(chan string, 2)
	device := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		bodies <- string(body)
		response.WriteHeader(http.StatusOK)
	}))
	defer device.Close()
	pusher := New(device.URL+"/api/push", "push-token", producer, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	pusher.Enqueue(namedSnapshot("superseded-before-request"))
	select {
	case <-renderStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first render did not start")
	}
	pusher.Enqueue(namedSnapshot("latest-before-request"))
	close(releaseRender)
	select {
	case body := <-bodies:
		if body != "latest-before-request" {
			t.Fatalf("device received %q, want latest frame", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("latest frame was not pushed")
	}
	waitForIdle(t, pusher)
	select {
	case body := <-bodies:
		t.Fatalf("superseded frame was also pushed: %q", body)
	case <-time.After(75 * time.Millisecond):
	}
	stopPusher(t, cancel, done)
}

func TestPusherRejectsPayloadOverFiveMiBWithoutSending(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	rendered := make(chan struct{})
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		close(rendered)
		return make([]byte, MaxPayloadBytes+1), nil
	}))
	pusher := New(server.URL+"/api/push", "push-token", producer, nil)
	pusher.retryDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()
	pusher.Enqueue(quota.Snapshot{DataUpdatedAt: time.Unix(4, 0), Accounts: []quota.Account{}})
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("oversize frame was not rendered")
	}
	waitForIdle(t, pusher)
	stopPusher(t, cancel, done)
	if requests.Load() != 0 {
		t.Fatalf("oversize frame generated %d HTTP requests, want 0", requests.Load())
	}
	pusher.mu.Lock()
	pending := pusher.pending != nil
	hasLast := pusher.hasLast
	hasRejected := pusher.hasRejected
	pusher.mu.Unlock()
	if pending || hasLast || !hasRejected {
		t.Fatalf("oversize state = pending %v successful-digest %v rejected-digest %v, want false/false/true", pending, hasLast, hasRejected)
	}
}

func TestPusherDoesNotRetryPermanentlyRejectedDigest(t *testing.T) {
	var requests atomic.Int32
	requestSeen := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		requestSeen <- struct{}{}
		response.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		return []byte("same rejected PNG"), nil
	}))
	pusher := New(server.URL+"/api/push", "wrong-token", producer, nil)
	pusher.retryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	snapshot := quota.Snapshot{DataUpdatedAt: time.Unix(5, 0), Accounts: []quota.Account{}}
	pusher.Enqueue(snapshot)
	select {
	case <-requestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("permanently rejected request did not start")
	}
	waitForIdle(t, pusher)
	time.Sleep(5 * pusher.retryDelay)
	if got := requests.Load(); got != 1 {
		t.Fatalf("HTTP 401 request count = %d, want no scheduled retry", got)
	}

	// A later refresh producing the same bytes is suppressed as well. A
	// different image digest remains eligible, which lets payload-specific 4xx
	// failures recover when the dashboard content changes.
	snapshot.DataUpdatedAt = snapshot.DataUpdatedAt.Add(time.Minute)
	pusher.Enqueue(snapshot)
	waitForIdle(t, pusher)
	time.Sleep(2 * pusher.retryDelay)
	if got := requests.Load(); got != 1 {
		t.Fatalf("same rejected digest request count = %d, want 1", got)
	}
	stopPusher(t, cancel, done)
}

func TestPusherDoesNotLoopOnRenderFailure(t *testing.T) {
	var renders atomic.Int32
	rendered := make(chan struct{}, 2)
	producer := frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
		renders.Add(1)
		rendered <- struct{}{}
		return nil, errors.New("renderer unavailable")
	}))
	pusher := New("http://192.0.2.10/api/push", "push-token", producer, nil)
	pusher.retryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pusher.Run(ctx)
	}()

	pusher.Enqueue(quota.Snapshot{DataUpdatedAt: time.Unix(6, 0), Accounts: []quota.Account{}})
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("render attempt did not start")
	}
	waitForIdle(t, pusher)
	time.Sleep(5 * pusher.retryDelay)
	if got := renders.Load(); got != 1 {
		t.Fatalf("render calls = %d, want no scheduled retry after permanent render failure", got)
	}
	stopPusher(t, cancel, done)
}

func TestPusherTreatsOnlyHTTP200AsSuccess(t *testing.T) {
	for _, test := range []struct {
		status    int
		permanent bool
	}{
		{status: http.StatusOK},
		{status: http.StatusUnauthorized, permanent: true},
		{status: http.StatusRequestEntityTooLarge, permanent: true},
		{status: http.StatusUnprocessableEntity, permanent: true},
		{status: http.StatusNotFound, permanent: true},
		{status: http.StatusFound, permanent: true},
		{status: http.StatusNoContent, permanent: true},
		{status: http.StatusRequestTimeout},
		{status: http.StatusConflict},
		{status: http.StatusLocked},
		{status: http.StatusTooEarly},
		{status: http.StatusTooManyRequests},
		{status: http.StatusServiceUnavailable},
	} {
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.status)
			}))
			defer server.Close()
			pusher := New(server.URL+"/api/push", "push-token", frame.NewProducer(rendererFunc(func(quota.Snapshot) ([]byte, error) {
				return nil, nil
			})), nil)
			err := pusher.post(context.Background(), []byte("png"))
			if test.status == http.StatusOK && err != nil {
				t.Fatalf("post() error = %v, want nil", err)
			}
			if test.status != http.StatusOK && err == nil {
				t.Fatalf("post() error = nil for HTTP %d", test.status)
			}
			var permanent *permanentPushError
			if got := errors.As(err, &permanent); got != test.permanent {
				t.Fatalf("HTTP %d permanent = %v, want %v (error %v)", test.status, got, test.permanent, err)
			}
		})
	}
}

func namedSnapshot(name string) quota.Snapshot {
	return quota.Snapshot{
		DataUpdatedAt: time.Now().UTC(),
		Accounts:      []quota.Account{},
		Errors:        []string{name},
	}
}

func accountsWithRemaining(remaining float64) []quota.Account {
	return []quota.Account{{
		Provider: "codex",
		Name:     "account",
		Status:   "ok",
		Windows:  []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(remaining)}},
	}}
}

func waitForIdle(t *testing.T, pusher *Pusher) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pusher.mu.Lock()
		idle := pusher.pending == nil
		pusher.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for pusher to become idle")
}

func stopPusher(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out stopping pusher")
	}
}
