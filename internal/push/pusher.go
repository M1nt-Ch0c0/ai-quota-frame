package push

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const (
	MaxPayloadBytes = 5 * 1024 * 1024
	// One render can perform five sequential 120-second BUSY waits. Keep the
	// client alive beyond that worst case so a completed refresh is not retried
	// merely because its HTTP 200 arrived after a shorter client deadline.
	RequestTimeout    = 11 * time.Minute
	defaultRetryDelay = 30 * time.Second
)

type Producer interface {
	Produce(quota.Snapshot) (frame.Image, error)
}

// Pusher keeps at most one pending snapshot. Enqueue replaces that pending
// snapshot, while Run serializes rendering and physical display refreshes.
type Pusher struct {
	endpoint   string
	token      string
	producer   Producer
	client     *http.Client
	logger     *slog.Logger
	retryDelay time.Duration

	runOnce      sync.Once
	wake         chan struct{}
	mu           sync.Mutex
	nextID       uint64
	pending      *queuedSnapshot
	last         [32]byte
	hasLast      bool
	rejected     [32]byte
	hasRejected  bool
	activeID     uint64
	activeCancel context.CancelFunc
}

type permanentPushError struct {
	err error
}

func (failure *permanentPushError) Error() string {
	return failure.err.Error()
}

func (failure *permanentPushError) Unwrap() error {
	return failure.err
}

type queuedSnapshot struct {
	id       uint64
	snapshot quota.Snapshot
}

func New(endpoint, token string, producer Producer, logger *slog.Logger) *Pusher {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Pusher{
		endpoint: endpoint,
		token:    token,
		producer: producer,
		client: &http.Client{
			Timeout:   RequestTimeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger:     logger,
		retryDelay: defaultRetryDelay,
		wake:       make(chan struct{}, 1),
	}
}

// Enqueue records the latest ready snapshot and returns without rendering or
// performing network I/O.
func (pusher *Pusher) Enqueue(snapshot quota.Snapshot) {
	pusher.mu.Lock()
	pusher.nextID++
	pusher.pending = &queuedSnapshot{id: pusher.nextID, snapshot: cloneSnapshot(snapshot)}
	pusher.mu.Unlock()
	pusher.signal()
}

// ObserveRefresh queues successful refreshes and invalidates all pending work
// after a failed refresh. Failed snapshots are never sent to the device, and a
// previous LIVE frame is not allowed to keep retrying after it became stale.
func (pusher *Pusher) ObserveRefresh(snapshot quota.Snapshot, successful bool) {
	if successful {
		pusher.Enqueue(snapshot)
		return
	}
	pusher.discardAll()
}

func (pusher *Pusher) signal() {
	select {
	case pusher.wake <- struct{}{}:
	default:
	}
}

func (pusher *Pusher) discardAll() {
	pusher.mu.Lock()
	pusher.nextID++
	pusher.pending = nil
	cancel := pusher.activeCancel
	pusher.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	pusher.signal()
}

// Run processes queued snapshots until ctx is cancelled. Calling Run more
// than once never creates concurrent workers.
func (pusher *Pusher) Run(ctx context.Context) {
	pusher.runOnce.Do(func() {
		pusher.run(ctx)
	})
}

func (pusher *Pusher) run(ctx context.Context) {
	var retryTimer *time.Timer
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()

	for {
		var retry <-chan time.Time
		if retryTimer != nil {
			retry = retryTimer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-pusher.wake:
			if retryTimer != nil {
				if !retryTimer.Stop() {
					select {
					case <-retryTimer.C:
					default:
					}
				}
				retryTimer = nil
			}
		case <-retry:
			retryTimer = nil
		}

		for {
			queued, ok := pusher.current()
			if !ok {
				break
			}
			pusher.drainWake()
			image, pushed, superseded, rejected, err := pusher.attempt(ctx, queued.id, queued.snapshot)
			if ctx.Err() != nil {
				return
			}
			if pushed {
				// HTTP 200 means the panel refresh completed. Record that physical
				// state even when a newer snapshot arrived during the request, so an
				// identical replacement does not refresh the panel a second time.
				pusher.complete(queued.id, image.Digest)
				pusher.logger.Info("PhotoPainter frame pushed", "bytes", len(image.Payload))
				continue
			}
			if superseded {
				continue
			}
			if rejected {
				pusher.discard(queued.id)
				continue
			}
			if err == nil {
				pusher.complete(queued.id, image.Digest)
				continue
			}

			var permanent *permanentPushError
			if errors.As(err, &permanent) {
				pusher.logger.Error("PhotoPainter frame push rejected; waiting for the next refresh", "error", err)
				pusher.reject(queued.id, image.Digest)
				continue
			}

			pusher.logger.Error("PhotoPainter frame push failed; retrying", "error", err)
			if !pusher.isCurrent(queued.id) {
				continue
			}
			delay := pusher.retryDelay
			if delay <= 0 {
				delay = defaultRetryDelay
			}
			retryTimer = time.NewTimer(delay)
			break
		}
	}
}

func (pusher *Pusher) drainWake() {
	select {
	case <-pusher.wake:
	default:
	}
}

func (pusher *Pusher) current() (queuedSnapshot, bool) {
	pusher.mu.Lock()
	defer pusher.mu.Unlock()
	if pusher.pending == nil {
		return queuedSnapshot{}, false
	}
	return *pusher.pending, true
}

func (pusher *Pusher) isCurrent(id uint64) bool {
	pusher.mu.Lock()
	defer pusher.mu.Unlock()
	return pusher.pending != nil && pusher.pending.id == id
}

func (pusher *Pusher) complete(id uint64, digest [32]byte) {
	pusher.mu.Lock()
	pusher.last = digest
	pusher.hasLast = true
	pusher.hasRejected = false
	if pusher.pending != nil && pusher.pending.id == id {
		pusher.pending = nil
	}
	pusher.mu.Unlock()
}

func (pusher *Pusher) discard(id uint64) {
	pusher.mu.Lock()
	if pusher.pending != nil && pusher.pending.id == id {
		pusher.pending = nil
	}
	pusher.mu.Unlock()
}

func (pusher *Pusher) reject(id uint64, digest [32]byte) {
	pusher.mu.Lock()
	if pusher.pending != nil && pusher.pending.id == id {
		pusher.rejected = digest
		pusher.hasRejected = true
		pusher.pending = nil
	}
	pusher.mu.Unlock()
}

func (pusher *Pusher) attempt(ctx context.Context, id uint64, snapshot quota.Snapshot) (frame.Image, bool, bool, bool, error) {
	image, err := pusher.producer.Produce(snapshot)
	if err != nil {
		if !pusher.isCurrent(id) {
			return frame.Image{}, false, true, false, nil
		}
		return frame.Image{}, false, false, false, &permanentPushError{err: fmt.Errorf("render frame: %w", err)}
	}
	if !pusher.isCurrent(id) {
		return image, false, true, false, nil
	}
	if pusher.wasPushed(image.Digest) {
		return image, false, false, false, nil
	}
	if pusher.wasRejected(image.Digest) {
		return image, false, false, true, nil
	}
	if len(image.Payload) > MaxPayloadBytes {
		return image, false, false, false, &permanentPushError{err: fmt.Errorf("frame PNG is %d bytes; maximum is %d", len(image.Payload), MaxPayloadBytes)}
	}
	superseded, err := pusher.postCurrent(ctx, id, image.Payload)
	if err != nil {
		if superseded {
			return image, false, true, false, nil
		}
		return image, false, false, false, err
	}
	return image, true, superseded, false, nil
}

func (pusher *Pusher) wasPushed(digest [32]byte) bool {
	pusher.mu.Lock()
	defer pusher.mu.Unlock()
	return pusher.hasLast && pusher.last == digest
}

func (pusher *Pusher) wasRejected(digest [32]byte) bool {
	pusher.mu.Lock()
	defer pusher.mu.Unlock()
	return pusher.hasRejected && pusher.rejected == digest
}

func (pusher *Pusher) postCurrent(ctx context.Context, id uint64, payload []byte) (bool, error) {
	requestContext, cancel := context.WithCancel(ctx)
	pusher.mu.Lock()
	if pusher.pending == nil || pusher.pending.id != id {
		pusher.mu.Unlock()
		cancel()
		return true, nil
	}
	pusher.activeID = id
	pusher.activeCancel = cancel
	pusher.mu.Unlock()

	err := pusher.post(requestContext, payload)
	cancel()

	pusher.mu.Lock()
	if pusher.activeID == id {
		pusher.activeID = 0
		pusher.activeCancel = nil
	}
	superseded := pusher.pending == nil || pusher.pending.id != id
	pusher.mu.Unlock()
	return superseded, err
}

func (pusher *Pusher) post(ctx context.Context, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, pusher.endpoint, bytes.NewReader(payload))
	if err != nil {
		return &permanentPushError{err: errors.New("create PhotoPainter push request")}
	}
	request.Header.Set("Content-Type", "image/png")
	request.Header.Set("Authorization", "Bearer "+pusher.token)
	request.ContentLength = int64(len(payload))

	response, err := pusher.client.Do(request)
	if err != nil {
		return fmt.Errorf("send PhotoPainter push request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		statusError := fmt.Errorf("PhotoPainter push returned HTTP %d", response.StatusCode)
		if retryableHTTPStatus(response.StatusCode) {
			return statusError
		}
		return &permanentPushError{err: statusError}
	}
	return nil
}

func retryableHTTPStatus(status int) bool {
	return status >= 500 && status <= 599 ||
		status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusLocked ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests
}

func cloneSnapshot(snapshot quota.Snapshot) quota.Snapshot {
	cloned := snapshot
	if snapshot.LastFreshAt != nil {
		value := *snapshot.LastFreshAt
		cloned.LastFreshAt = &value
	}
	cloned.Accounts = append([]quota.Account(nil), snapshot.Accounts...)
	for accountIndex := range cloned.Accounts {
		account := &cloned.Accounts[accountIndex]
		if account.LastFreshAt != nil {
			value := *account.LastFreshAt
			account.LastFreshAt = &value
		}
		account.Windows = append([]quota.Window(nil), account.Windows...)
		for windowIndex := range account.Windows {
			window := &account.Windows[windowIndex]
			if window.UsedPercent != nil {
				value := *window.UsedPercent
				window.UsedPercent = &value
			}
			if window.RemainingPercent != nil {
				value := *window.RemainingPercent
				window.RemainingPercent = &value
			}
			if window.ResetsAt != nil {
				value := *window.ResetsAt
				window.ResetsAt = &value
			}
			if window.ObservedAt != nil {
				value := *window.ObservedAt
				window.ObservedAt = &value
			}
		}
	}
	if snapshot.Usage != nil {
		usage := *snapshot.Usage
		usage.Days = append([]quota.UsageDay(nil), snapshot.Usage.Days...)
		cloned.Usage = &usage
	}
	cloned.Errors = append([]string(nil), snapshot.Errors...)
	return cloned
}
