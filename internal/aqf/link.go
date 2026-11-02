// Package aqf keeps the daemon's single authenticated PhotoPainter BLE session
// open and shares it between image pushes, the console and the aqf command
// line, in the way adb keeps device transports in its server.
package aqf

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

// Device is the subset of *ble.Client used through a persistent session.
type Device interface {
	Info() ble.DeviceInfo
	Ping(context.Context) error
	Status(context.Context) (json.RawMessage, error)
	Shell(context.Context, string) (ble.ShellResult, error)
}

// Dialer runs job for the lifetime of one authenticated connection. ble.WithClient
// runs it on the radio goroutine, so every native call stays on that thread.
type Dialer func(ctx context.Context, job func(context.Context, Device) error) error

type LinkState struct {
	DeviceID    string          `json:"device_id"`
	Connected   bool            `json:"connected"`
	Since       time.Time       `json:"since,omitzero"`
	LastError   string          `json:"last_error,omitempty"`
	Connections int             `json:"connections"`
	Busy        bool            `json:"busy,omitempty"`
	Info        *ble.DeviceInfo `json:"info,omitempty"`
}

type call struct {
	ctx       context.Context
	work      func(context.Context, Device) error
	exclusive func(context.Context) error
	done      chan error
}

type Link struct {
	id        string
	dial      Dialer
	keepalive time.Duration
	idle      time.Duration // zero keeps the session until disconnect or failure
	calls     chan call
	drop      chan struct{}

	mu    sync.Mutex
	state LinkState
	next  *call // an exclusive call that ended the previous session
}

func NewLink(id string, dial Dialer, keepalive, idle time.Duration) *Link {
	return &Link{id: id, dial: dial, keepalive: keepalive, idle: idle,
		calls: make(chan call), drop: make(chan struct{}, 1), state: LinkState{DeviceID: id}}
}

func (l *Link) State() LinkState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// Do runs work on the connected device, connecting first when needed. Work is
// never replayed after a failure: the caller decides whether a retry is safe.
func (l *Link) Do(ctx context.Context, work func(context.Context, Device) error) error {
	c := call{ctx: ctx, work: work, done: make(chan error, 1)}
	select {
	case l.calls <- c:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-c.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Exclusive closes the session and runs work while no session can be opened,
// for operations that use the radio directly (scan, pairing) or reset the
// device (USB flashing). The next Do reconnects.
func (l *Link) Exclusive(ctx context.Context, work func(context.Context) error) error {
	c := call{ctx: ctx, exclusive: work, done: make(chan error, 1)}
	select {
	case l.calls <- c:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-c.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Link) runExclusive(c call) {
	l.setBusy(true)
	c.done <- c.exclusive(c.ctx)
	l.setBusy(false)
}

// WithClient has the shape of ble.WithClient, so the image sender and console
// queue run their work on the shared session instead of a new connection.
func (l *Link) WithClient(ctx context.Context, job func(context.Context, *ble.Client) error) error {
	return l.Do(ctx, func(ctx context.Context, d Device) error {
		c, ok := d.(*ble.Client)
		if !ok {
			return errors.New("BLE session has no native client")
		}
		return job(ctx, c)
	})
}

// DialBLE authenticates with the saved pairing (or a legacy v1 token).
func DialBLE(id, token string) Dialer {
	return func(ctx context.Context, job func(context.Context, Device) error) error {
		return ble.WithClient(ctx, id, token, func(ctx context.Context, c *ble.Client) error { return job(ctx, c) })
	}
}

// Disconnect closes the current session; the next Do reconnects.
func (l *Link) Disconnect() {
	select {
	case l.drop <- struct{}{}:
	default:
	}
}

func (l *Link) Run(ctx context.Context) {
	for {
		l.mu.Lock()
		next := l.next
		l.next = nil
		l.mu.Unlock()
		if next != nil {
			l.runExclusive(*next)
			continue
		}
		var first call
		select {
		case <-ctx.Done():
			return
		case <-l.drop:
			continue
		case first = <-l.calls:
		}
		if first.exclusive != nil {
			l.runExclusive(first)
			continue
		}
		l.setBusy(true)
		// A timed-out dial can still connect later on the radio goroutine, so
		// exactly one side claims the first call's reply.
		var claimed atomic.Bool
		err := l.dial(ctx, func(ctx context.Context, d Device) error {
			if !claimed.CompareAndSwap(false, true) {
				return ctx.Err()
			}
			l.connected(d)
			return l.serve(ctx, d, first)
		})
		if claimed.CompareAndSwap(false, true) {
			if err == nil {
				err = errors.New("BLE session closed")
			}
			first.done <- err
		}
		l.setBusy(false)
		l.disconnected(err)
	}
}

func (l *Link) serve(ctx context.Context, d Device, first call) error {
	run := func(c call) error {
		l.setBusy(true)
		err := c.work(c.ctx, d)
		l.setBusy(false)
		c.done <- err
		var rejected ble.DeviceError
		if err != nil && !errors.As(err, &rejected) && !errors.Is(err, ble.ErrShellUnsupported) {
			return err // a transport failure invalidates the session sequence
		}
		return nil
	}
	if err := run(first); err != nil {
		return err
	}
	ticker := time.NewTicker(l.keepalive)
	defer ticker.Stop()
	var idle <-chan time.Time
	var idleTimer *time.Timer
	if l.idle > 0 {
		idleTimer = time.NewTimer(l.idle)
		defer idleTimer.Stop()
		idle = idleTimer.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-l.drop:
			return nil
		case <-idle:
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := d.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		case c := <-l.calls:
			if c.exclusive != nil {
				l.mu.Lock()
				l.next = &c
				l.mu.Unlock()
				return nil
			}
			if err := run(c); err != nil {
				return err
			}
			if idleTimer != nil {
				idleTimer.Reset(l.idle)
			}
		}
	}
}

func (l *Link) setBusy(busy bool) {
	l.mu.Lock()
	l.state.Busy = busy
	l.mu.Unlock()
}

func (l *Link) connected(d Device) {
	info := d.Info()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state.Connected, l.state.Since, l.state.LastError, l.state.Info = true, time.Now(), "", &info
	l.state.Connections++
}

func (l *Link) disconnected(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state.Connected, l.state.Since = false, time.Time{}
	if err != nil && !errors.Is(err, context.Canceled) {
		l.state.LastError = err.Error()
	}
}
