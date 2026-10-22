//go:build photopainter_hardware && (windows || linux || (darwin && cgo))

package ble

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// This separate opt-in performs exactly one physical refresh. It never retries
// a request whose final response is lost. Human visual confirmation is still
// required after HTTP 200. Keep the automatic pusher/watchdog stopped.
func TestHardwareHTTPDisplay(t *testing.T) {
	mode := os.Getenv("PHOTOFRAME_HARDWARE_TEST")
	if mode != "display-once" && mode != "http-rejections" {
		t.Skip("explicit one-display hardware opt-in required")
	}
	var payload []byte
	var err error
	if mode == "display-once" {
		payload, err = os.ReadFile(os.Getenv("PHOTOFRAME_HARDWARE_PNG"))
		if err != nil || len(payload) != 5*1024*1024 || payload[28] != 0 {
			t.Fatal("explicit 5 MiB validation PNG required")
		}
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil || img.Bounds().Dx() != 800 || img.Bounds().Dy() != 480 {
			t.Fatal("invalid validation PNG geometry")
		}
		allowed := map[[4]uint32]bool{{0, 0, 0, 65535}: true, {65535, 65535, 65535, 65535}: true, {65535, 65535, 0, 65535}: true, {65535, 0, 0, 65535}: true, {0, 0, 65535, 65535}: true, {0, 65535, 0, 65535}: true}
		for y := 0; y < 480; y++ {
			for x := 0; x < 800; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if !allowed[[4]uint32{r, g, b, a}] {
					t.Fatal("validation PNG is not opaque six-color")
				}
			}
		}
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Minute)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ExpectContinueTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 11 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		url, err := c.PushURL()
		if err != nil {
			return err
		}
		before, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		ready := false
		for _, app := range before.Apps {
			ready = ready || (app.ID == "photoframe" && app.Bank == before.Loaded)
		}
		if !ready {
			return errors.New("select the original photoframe before the display test")
		}
		send := func(body io.Reader, length int64, auth string, expect bool) (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
			if err != nil {
				return 0, err
			}
			req.ContentLength = length
			req.Header.Set("Content-Type", "image/png")
			if auth != "" {
				req.Header.Set("Authorization", "Bearer "+auth)
			}
			if expect {
				req.Header.Set("Expect", "100-continue")
			}
			res, err := httpClient.Do(req)
			if err != nil {
				return 0, errors.New("HTTP transport failed; do not repeat the physical push automatically")
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			return res.StatusCode, nil
		}
		wrong := "!" + token[1:]
		if wrong == token {
			wrong = "\"" + token[1:]
		}
		for _, check := range []struct {
			name, auth string
			body       []byte
			want       int
		}{
			{"missing token", "", make([]byte, 8), 401},
			{"wrong token", wrong, make([]byte, 8), 401},
			{"oversize", token, nil, 413},
			{"invalid PNG", token, make([]byte, 8), 422},
		} {
			var code int
			var err error
			if check.want == 413 {
				code, err = hwOversizeHTTP(ctx, url, token)
			} else {
				code, err = send(bytes.NewReader(check.body), int64(len(check.body)), check.auth, false)
			}
			if err != nil {
				return fmt.Errorf("before physical push, %s probe: %w", check.name, err)
			}
			if code != check.want {
				return fmt.Errorf("%s: expected HTTP %d, received %d", check.name, check.want, code)
			}
			t.Logf("HTTP %d: %s rejected", code, check.name)
		}
		if mode == "http-rejections" {
			return nil
		}
		hwMemory(t, "before-display", before)
		gate := &hardwareBodyGate{reader: bytes.NewReader(payload), ctx: ctx, paused: make(chan struct{}), resume: make(chan struct{})}
		type result struct {
			code int
			err  error
		}
		finished := make(chan result, 1)
		started := time.Now()
		go func() { code, err := send(gate, int64(len(payload)), token, false); finished <- result{code, err} }()
		select {
		case <-gate.paused:
		case r := <-finished:
			return fmt.Errorf("HTTP ended before body gate: status %d, error %v", r.code, r.err)
		case <-ctx.Done():
			return ctx.Err()
		}
		var observationErr error
		func() {
			defer close(gate.resume) // Always let the single request finish.
			for attempt := 0; attempt < 20; attempt++ {
				state, err := hwStatus(ctx, c)
				if err != nil {
					observationErr = err
					return
				}
				if before.FreePSRAM > state.FreePSRAM+5*1024*1024-4096 {
					hwMemory(t, "receiving-5MiB-PNG", state)
					begin := make([]byte, 68)
					binary.LittleEndian.PutUint32(begin, 4096)
					copy(begin[36:], "ble-echo")
					_, err = c.command(ctx, OpBegin, begin)
					observationErr = hwExpect(err, 3)
					if observationErr == nil {
						t.Log("BLE upload rejected busy during HTTP reception")
					}
					return
				}
				if err := wait(ctx, 100*time.Millisecond); err != nil {
					observationErr = err
					return
				}
			}
			observationErr = errors.New("5 MiB HTTP reception allocation was not observed")
		}()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case r := <-finished:
				if r.err != nil {
					return r.err
				}
				t.Logf("PHYSICAL HTTP %d after %.1f seconds; human visual confirmation required", r.code, time.Since(started).Seconds())
				if r.code != 200 {
					return fmt.Errorf("physical push returned HTTP %d; not retried", r.code)
				}
				if observationErr != nil {
					return observationErr
				}
				after, err := hwStatus(ctx, c)
				if err == nil {
					hwMemory(t, "after-display", after)
				}
				return err
			case <-ticker.C:
				// Keep this authenticated session alive while the manager refreshes.
				state, err := hwStatus(ctx, c)
				if err != nil {
					observationErr = err
				} else {
					hwMemory(t, "display-in-progress", state)
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Send the oversized Content-Length and one invalid body byte. IDF dispatches
// its handler from the first body callback. Some embedded servers send
// 100 Continue before the handler's 413, and a general HTTP client then races a
// large body write against the connection close. This checks rejection at the
// start of the body, before a valid image or display can be accepted.
func hwOversizeHTTP(ctx context.Context, target, token string) (int, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" {
		return 0, errors.New("invalid hardware HTTP target")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), "80"))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return 0, err
	}
	_, err = fmt.Fprintf(conn, "POST /api/push HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: image/png\r\nContent-Length: 5242881\r\nConnection: close\r\n\r\n\x00", u.Host, token)
	if err != nil {
		return 0, errors.New("oversize header write failed")
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

type hardwareBodyGate struct {
	reader         *bytes.Reader
	ctx            context.Context
	paused, resume chan struct{}
	sent           bool
	continued      bool
}

func (r *hardwareBodyGate) Read(p []byte) (int, error) {
	if r.sent && !r.continued {
		r.continued = true
		close(r.paused)
		select {
		case <-r.resume:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	r.sent = true
	return r.reader.Read(p)
}
