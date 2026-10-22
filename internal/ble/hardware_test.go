//go:build photopainter_hardware && (windows || linux || (darwin && cgo))

package ble

// Explicit opt-in hardware checks. Normal make check never builds this file.
// These tests stage and remove a new ble-echo candidate, never activate it or
// alter an existing application's slots. Stop the pusher/watchdog first.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type hardwareState struct {
	Loaded       int    `json:"loaded_app"`
	UptimeMS     uint64 `json:"uptime_ms"`
	ResetReason  int    `json:"reset_reason"`
	FreeInternal uint32 `json:"free_internal_ram"`
	FreePSRAM    uint32 `json:"free_psram"`
	MinInternal  uint32 `json:"minimum_free_internal_ram"`
	MinPSRAM     uint32 `json:"minimum_free_psram"`
	Apps         []struct {
		ID      string `json:"id"`
		Bank    int    `json:"bank"`
		Active  int    `json:"active"`
		Pending int    `json:"pending"`
	} `json:"apps"`
}

func hwStatus(ctx context.Context, c *Client) (hardwareState, error) {
	p, err := c.Status(ctx)
	if err != nil {
		return hardwareState{}, err
	}
	var state hardwareState
	err = json.Unmarshal(p, &state)
	return state, err
}
func hwMemory(t *testing.T, label string, state hardwareState) {
	t.Logf("MEMORY %s internal=%d psram=%d minimum_internal=%d minimum_psram=%d uptime_ms=%d reset_reason=%d", label, state.FreeInternal, state.FreePSRAM, state.MinInternal, state.MinPSRAM, state.UptimeMS, state.ResetReason)
}
func hwExpect(err error, code DeviceError) error {
	var deviceErr DeviceError
	if !errors.As(err, &deviceErr) || deviceErr != code {
		return fmt.Errorf("expected BLE error %d, received %v", code, err)
	}
	return nil
}

func hwHTTPBusy(ctx context.Context, c *Client, token string) error {
	url, err := c.PushURL()
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, url, bytes.NewReader([]byte("\x89PNG\r\n\x1a\n")))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "image/png")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("HTTP busy probe could not reach the authenticated device IP")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		return fmt.Errorf("HTTP during BLE upload: expected 409, received %d", response.StatusCode)
	}
	return nil
}

func TestHardwareDiscoveryDeadline(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "discovery-only" {
		t.Skip("explicit discovery opt-in required")
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		url, err := DiscoverPushURL(ctx, os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN"))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("authenticated discovery %d: %.1fs, target=%s", attempt+1, time.Since(start).Seconds(), url)
	}
}

func TestHardwareBLEUpload(t *testing.T) {
	if os.Getenv("PHOTOFRAME_HARDWARE_TEST") != "stage-only" {
		t.Skip("explicit hardware opt-in required")
	}
	id, token := os.Getenv("PHOTOFRAME_DEVICE_ID"), os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	if err := validateToken(token); err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile(os.Getenv("PHOTOFRAME_HARDWARE_PACKAGE"))
	if err != nil {
		t.Fatal("read explicit test package")
	}
	metadata, err := InspectPackage(pkg)
	if err != nil || metadata.App != "ble-echo" {
		t.Fatal("a valid ble-echo package is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	var called atomic.Bool
	wrong := []byte(token)
	if wrong[0] == '!' {
		wrong[0] = '"'
	} else {
		wrong[0] = '!'
	}
	err = WithClient(ctx, id, string(wrong), func(context.Context, *Client) error { called.Store(true); return nil })
	if !errors.Is(err, ErrAuthentication) || called.Load() {
		t.Fatalf("wrong-token test did not reach authentication rejection: %v", err)
	}
	t.Log("wrong-token authentication rejected")
	if err := withNativeLink(ctx, id, func(link *nativeLink) error {
		info, err := link.Info(ctx)
		if err != nil {
			return err
		}
		if _, err = ParseInfo(info); err != nil {
			return err
		}
		request := make([]byte, 41)
		request[0] = OpStatus
		binary.LittleEndian.PutUint32(request[1:], 1)
		response, err := link.Exchange(ctx, request)
		if err != nil {
			return err
		}
		if len(response) != 7 || response[5] != 1 {
			return errors.New("unauthenticated status was not rejected")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("unauthenticated discovery allowed; application status rejected")
	var cleanupAllowed atomic.Bool
	t.Cleanup(func() {
		if !cleanupAllowed.Load() {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := WithClient(cleanupCtx, id, token, func(ctx context.Context, c *Client) error {
			state, err := hwStatus(ctx, c)
			if err != nil {
				return err
			}
			for _, app := range state.Apps {
				if app.ID == "ble-echo" && app.Active < 0 {
					return c.Manage(ctx, OpRemove, "ble-echo")
				}
			}
			return nil
		}); err != nil {
			t.Errorf("pending test candidate cleanup needs attention: %v", err)
		}
	})
	err = WithClient(ctx, id, token, func(ctx context.Context, c *Client) error {
		before, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		for _, app := range before.Apps {
			if app.ID == "ble-echo" {
				return errors.New("ble-echo already exists; refusing to overwrite it")
			}
		}
		cleanupAllowed.Store(true)
		hwMemory(t, "connected-idle", before)
		if native, ok := c.link.(*nativeLink); ok {
			t.Logf("ATT write capacity: %d bytes", native.writeSize)
		}
		// A replayed valid tag must not consume the next sequence or run a job.
		replay := make([]byte, 5)
		replay[0] = OpInfo
		binary.LittleEndian.PutUint32(replay[1:], c.seq)
		replay = append(replay, tag(c.key, "pp-ble-v1/request", replay)...)
		response, err := c.link.Exchange(ctx, replay)
		if err != nil {
			return err
		}
		if len(response) != 7 || response[5] != 1 {
			return errors.New("replayed command accepted")
		}
		if _, err = c.command(ctx, OpInfo, nil); err != nil {
			return err
		}
		t.Log("replay rejected; valid next sequence accepted")
		appID, _ := encodeApp("ble-echo")
		begin := make([]byte, 68)
		binary.LittleEndian.PutUint32(begin, uint32(len(pkg)))
		digest := sha256.Sum256(pkg)
		copy(begin[4:], digest[:])
		copy(begin[36:], appID)
		begin[4] ^= 1 // Deliberate whole-package hash mismatch.
		if _, err = c.command(ctx, OpBegin, begin); err != nil {
			return err
		}
		if os.Getenv("PHOTOFRAME_HARDWARE_HTTP") == "busy-only" {
			if err = hwHTTPBusy(ctx, c, token); err != nil {
				return err
			}
			t.Log("HTTP push rejected with 409 while BLE owns manager admission")
		}
		chunk := make([]byte, 404)
		copy(chunk[4:], pkg[:400])
		if _, err = c.command(ctx, OpChunk, chunk); err != nil {
			return err
		}
		if _, err = c.command(ctx, OpChunk, chunk); err != nil {
			return err
		}
		chunk[4] ^= 1
		_, err = c.command(ctx, OpChunk, chunk)
		if err = hwExpect(err, 5); err != nil {
			return err
		}
		chunk[4] ^= 1
		binary.LittleEndian.PutUint32(chunk, 401)
		_, err = c.command(ctx, OpChunk, chunk)
		if err = hwExpect(err, 5); err != nil {
			return err
		}
		_, err = c.command(ctx, OpActivate, appID)
		if err = hwExpect(err, 3); err != nil {
			return err
		}
		progress, err := c.command(ctx, OpProgress, nil)
		if err != nil || len(progress) != 8 || binary.LittleEndian.Uint32(progress) != 400 {
			return errors.New("unexpected upload progress")
		}
		if _, err = c.command(ctx, OpAbort, nil); err != nil {
			return err
		}
		t.Log("duplicate chunks accepted; gaps/conflicts rejected; activation busy; abort released admission")
		// Use a short valid package prefix plus an independent advertised hash
		// to test failed commit without writing Flash or wasting a maximum transfer.
		short := pkg
		if len(short) > 6000 {
			short = short[:6000]
		}
		binary.LittleEndian.PutUint32(begin, uint32(len(short)))
		if _, err = c.command(ctx, OpBegin, begin); err != nil {
			return err
		}
		for offset := 0; offset < len(short); {
			end := min(offset+400, len(short))
			p := make([]byte, 4)
			binary.LittleEndian.PutUint32(p, uint32(offset))
			p = append(p, short[offset:end]...)
			if _, err = c.command(ctx, OpChunk, p); err != nil {
				return err
			}
			offset = end
		}
		_, err = c.command(ctx, OpCommit, nil)
		if err = hwExpect(err, 6); err != nil {
			return err
		}
		afterBad, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		if len(afterBad.Apps) != len(before.Apps) || afterBad.Loaded != before.Loaded {
			return errors.New("bad hash changed application catalog")
		}
		t.Log("SHA mismatch rejected without a catalog change")
		last := -1
		uploadStarted := time.Now()
		var observationErr error
		err = c.Install(ctx, pkg, func(done, total int) {
			if done == 400 {
				state, e := hwStatus(ctx, c)
				if e != nil {
					observationErr = e
				} else {
					hwMemory(t, "receiving-package", state)
				}
			}
			percent := done * 100 / total
			if percent != last {
				last = percent
				t.Logf("UPLOAD %d%% (%d/%d) elapsed=%.1fs", percent, done, total, time.Since(uploadStarted).Seconds())
			}
		})
		if err != nil {
			return err
		}
		if observationErr != nil {
			return observationErr
		}
		staged, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "staged", staged)
		if staged.Loaded != before.Loaded {
			return errors.New("staging switched the running application")
		}
		found := false
		for _, app := range staged.Apps {
			if app.ID == "ble-echo" {
				found = app.Active == -1 && app.Pending >= 0
			}
		}
		if !found {
			return errors.New("candidate was not staged explicitly")
		}
		if err = c.Manage(ctx, OpRemove, "ble-echo"); err != nil {
			return err
		}
		final, err := hwStatus(ctx, c)
		if err != nil {
			return err
		}
		hwMemory(t, "removed-test-candidate", final)
		if len(final.Apps) != len(before.Apps) || final.Loaded != before.Loaded {
			return errors.New("test candidate cleanup failed")
		}
		t.Log("maximum package staged; original app unchanged; test candidate removed")
		cleanupAllowed.Store(false)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
