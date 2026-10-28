package ble

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"sync/atomic"
	"testing"
)

type appImageDevice struct {
	generation                                                    uint32
	unavailable, dropCommit, dropRead, dropChunk, corrupt, badAck bool
	result                                                        uint16
	state                                                         byte
	connections, begins, chunks, commits, queries, renders        int
	receipts                                                      map[[16]byte][]byte
	ids                                                           [][16]byte
	afterCommit                                                   func()
	busyWrite, busyRead                                           bool
}

func newAppImageDevice() *appImageDevice {
	return &appImageDevice{generation: 7, receipts: make(map[[16]byte][]byte)}
}
func (d *appImageDevice) client(t *testing.T) *Client {
	t.Helper()
	d.connections++
	f := newFake()
	f.info[1] = 0
	clear(f.info[8:12])
	count := 2
	if d.unavailable {
		count = 0
	}
	f.appStatus = []byte(fmt.Sprintf(`{"generation":%d,"ble_service_revision":9,"ble_characteristics":%d,"ble_service_uuid":"%s","runtime_ready":true,"loaded_app":0,"apps":[{"bank":0,"id":"photoframe"}]}`, d.generation, count, appImageUUID))
	var response, received []byte
	f.handle = func(op byte, p []byte) ([]byte, DeviceError, bool, error) {
		if op != OpStatus && op != OpAppRead && op != OpAppWrite {
			t.Fatalf("primary image path used host opcode %d", op)
		}
		if op == OpStatus {
			return nil, 0, false, nil
		}
		if op == OpAppWrite && d.busyWrite {
			d.busyWrite = false
			return nil, 3, true, nil
		}
		if op == OpAppRead && d.busyRead {
			d.busyRead = false
			return nil, 3, true, nil
		}
		if len(p) < 12 || binary.LittleEndian.Uint32(p) != d.generation || binary.LittleEndian.Uint32(p[4:]) != 9 {
			return nil, 8, true, nil
		}
		index := binary.LittleEndian.Uint32(p[8:])
		if op == OpAppRead {
			if index != 1 || len(p) != 12 {
				t.Fatal("invalid signed status read")
			}
			if d.dropRead && response[5] == appImageCommit {
				d.dropRead = false
				return nil, 0, true, errors.New("status read reply lost")
			}
			r := bytes.Clone(response)
			if d.corrupt {
				r[32] ^= 1
			}
			return r, 0, true, nil
		}
		wire := p[12:]
		if index != 0 || len(wire) < 18 || len(wire) > 128 || wire[0] != 1 {
			t.Fatal("invalid command envelope")
		}
		var id [16]byte
		copy(id[:], wire[2:18])
		command := wire[1]
		switch command {
		case appImageBegin:
			d.begins++
			d.ids = append(d.ids, id)
			if len(wire) != 54 {
				t.Fatal("invalid begin")
			}
			response = make([]byte, 64)
			copy(response, "PFI1")
			response[4] = appImageReceiving
			copy(response[8:], id[:])
			copy(response[24:], wire[18:22])
			copy(response[32:], wire[22:])
			received = nil
		case appImageChunk:
			d.chunks++
			if len(wire) <= 22 || binary.LittleEndian.Uint32(wire[18:]) != uint32(len(received)) {
				t.Fatal("invalid chunk offset")
			}
			if d.dropChunk {
				d.dropChunk = false
				return nil, 0, true, errors.New("partial receive disconnected")
			}
			received = append(received, wire[22:]...)
			binary.LittleEndian.PutUint32(response[28:], uint32(len(received)))
			if d.badAck {
				binary.LittleEndian.PutUint32(response[28:], uint32(len(received)+1))
			}
		case appImageCommit:
			d.commits++
			digest := sha256.Sum256(received)
			if len(wire) != 18 || binary.LittleEndian.Uint32(response[24:]) != uint32(len(received)) || !bytes.Equal(response[32:], digest[:]) {
				t.Fatal("incomplete or corrupt commit")
			}
			d.renders++
			response[4] = appImageComplete
			if d.result != 0 {
				response[4] = appImageFailed
			}
			if d.state != 0 {
				response[4] = d.state
			}
			binary.LittleEndian.PutUint16(response[6:], d.result)
			d.receipts[id] = bytes.Clone(response)
			if d.afterCommit != nil {
				d.afterCommit()
			}
			if d.dropCommit {
				d.dropCommit = false
				return nil, 0, true, errors.New("commit reply lost")
			}
		case appImageQuery:
			d.queries++
			response = bytes.Clone(d.receipts[id])
			if len(response) == 0 {
				response = make([]byte, 64)
				copy(response, "PFI1")
				copy(response[8:], id[:])
				response[4] = appImageMissing
			}
		default:
			t.Fatalf("unexpected command %d", command)
		}
		response[5] = command
		return nil, 0, true, nil
	}
	return connectFake(t, f)
}
func appSender(t *testing.T, d *appImageDevice) *AppImageSender {
	t.Helper()
	s, err := NewAppImageSender("000102030405", testToken, "photoframe")
	if err != nil {
		t.Fatal(err)
	}
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		return job(ctx, d.client(t))
	}
	return s
}
func requirePermanent(t *testing.T, err error) {
	t.Helper()
	var p interface{ Permanent() bool }
	if err == nil || !errors.As(err, &p) || !p.Permanent() {
		t.Fatal("expected permanent error", err)
	}
}
func TestUnpairedImageStopsImmediatelyButCanSendAfterPairing(t *testing.T) {
	d := newAppImageDevice()
	s, err := NewAppImageSender("000102030405", "", "photoframe")
	if err != nil {
		t.Fatal("fixed token still required", err)
	}
	count := 0
	s.withClient = func(context.Context, string, string, func(context.Context, *Client) error) error {
		count++
		return ErrNotPaired
	}
	data := testPNG(t, color.Black)
	err = s.Send(context.Background(), data, nil)
	if !errors.Is(err, ErrNotPaired) || count != 1 {
		t.Fatal("pairing requirement was retried", err, count)
	}
	requirePermanent(t, err)
	var nextRefresh interface{ RetryOnRefresh() bool }
	if !errors.As(err, &nextRefresh) || !nextRefresh.RetryOnRefresh() {
		t.Fatal("outer queue would permanently reject the PNG before pairing")
	}
	if len(s.blocked) != 0 {
		t.Fatal("unpaired error permanently blocked an image")
	}
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		return job(ctx, d.client(t))
	}
	if err = s.Send(context.Background(), data, nil); err != nil {
		t.Fatal("newly paired sender could not retry user intent", err)
	}
	if d.renders != 1 {
		t.Fatal("unexpected physical render count", d.renders)
	}
}
func TestAppImageSignedServiceAndReceiptRecovery(t *testing.T) {
	for _, lost := range []string{"write", "read"} {
		t.Run(lost, func(t *testing.T) {
			d := newAppImageDevice()
			d.dropCommit = lost == "write"
			d.dropRead = lost == "read"
			s := appSender(t, d)
			data := testPNG(t, color.Black)
			var acknowledged int
			if err := s.Send(context.Background(), data, func(n, total int) {
				if total != len(data) || n <= acknowledged {
					t.Fatal("bad progress")
				}
				acknowledged = n
			}); !errors.Is(err, ErrImagePending) {
				t.Fatal(err)
			}
			if acknowledged != len(data) {
				t.Fatal("missing progress")
			}
			chunks := d.chunks
			if err := s.Send(context.Background(), data, nil); err != nil {
				t.Fatal(err)
			}
			if d.commits != 1 || d.renders != 1 || d.queries != 1 || d.begins != 1 || d.chunks != chunks {
				t.Fatalf("repainted %+v", d)
			}
			connections := d.connections
			if err := s.Send(context.Background(), data, nil); err != nil || d.connections != connections {
				t.Fatal("dedup failed", err)
			}
		})
	}
}
func TestAppImagePreCommitLossUsesFreshID(t *testing.T) {
	d := newAppImageDevice()
	d.dropChunk = true
	s := appSender(t, d)
	data := testPNG(t, color.Black)
	if err := s.Send(context.Background(), data, nil); err == nil || errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), data, nil); err != nil {
		t.Fatal(err)
	}
	if d.ids[0] == d.ids[1] || d.renders != 1 || d.commits != 1 {
		t.Fatal("reused canceled pre-COMMIT intent")
	}
}

func TestAppImageRetriesExplicitSharedAdmissionBusy(t *testing.T) {
	d := newAppImageDevice()
	d.busyWrite, d.busyRead = true, true
	s := appSender(t, d)
	if err := s.Send(context.Background(), testPNG(t, color.Black), nil); err != nil {
		t.Fatal(err)
	}
	if d.busyWrite || d.busyRead || d.begins != 1 || d.commits != 1 || d.renders != 1 {
		t.Fatal("BUSY was not handled before command admission")
	}
}
func TestAppImageUnknownOutcomeStopsAcrossSnapshotReplacement(t *testing.T) {
	for _, mode := range []string{"switch", "restart-same-target", "evicted", "mismatch", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			d := newAppImageDevice()
			d.dropCommit = true
			s := appSender(t, d)
			data := testPNG(t, color.Black)
			if err := s.Send(context.Background(), data, nil); !errors.Is(err, ErrImagePending) {
				t.Fatal(err)
			}
			switch mode {
			case "switch":
				d.generation++
			case "restart-same-target", "evicted":
				clear(d.receipts)
			case "mismatch":
				d.corrupt = true
			case "unavailable":
				d.unavailable = true
			}
			err := s.Send(context.Background(), data, nil)
			requirePermanent(t, err)
			d.corrupt = false
			d.unavailable = false
			if err = s.Send(context.Background(), testPNG(t, color.White), nil); err != nil {
				t.Fatal(err)
			}
			requirePermanent(t, s.Send(context.Background(), data, nil))
			if d.renders != 2 || d.commits != 2 {
				t.Fatal("ambiguous image automatically repainted")
			}
		})
	}
}
func TestAppImagePendingSurvivesNewSnapshot(t *testing.T) {
	d := newAppImageDevice()
	d.dropCommit = true
	s := appSender(t, d)
	black := testPNG(t, color.Black)
	if err := s.Send(context.Background(), black, nil); !errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), testPNG(t, color.White), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), black, nil); err != nil {
		t.Fatal(err)
	}
	if d.renders != 2 || d.commits != 2 || d.queries != 1 {
		t.Fatal("lost old receipt identity")
	}
}
func TestAppImageUnavailableIsTransientAndNeverMutates(t *testing.T) {
	d := newAppImageDevice()
	d.unavailable = true
	s := appSender(t, d)
	data := testPNG(t, color.Black)
	if err := s.Send(context.Background(), data, nil); !errors.Is(err, ErrAppImageUnavailable) || d.begins != 0 {
		t.Fatal(err)
	}
	d.unavailable = false
	if err := s.Send(context.Background(), data, nil); err != nil {
		t.Fatal(err)
	}
}
func TestAppImageFailureAndMissingPhysicalReceiptNeverSucceed(t *testing.T) {
	for _, mode := range []string{"display", "processing", "incomplete", "bad-ack"} {
		t.Run(mode, func(t *testing.T) {
			d := newAppImageDevice()
			switch mode {
			case "display":
				d.result = 8
			case "processing":
				d.state = appImageProcessing
			case "incomplete":
				d.state = appImageReceiving
			case "bad-ack":
				d.badAck = true
			}
			s := appSender(t, d)
			data := testPNG(t, color.Black)
			err := s.Send(context.Background(), data, nil)
			if mode == "processing" {
				if !errors.Is(err, ErrImagePending) {
					t.Fatal(err)
				}
				_ = s.Send(context.Background(), data, nil)
				if d.commits != 1 {
					t.Fatal("processing commit repeated")
				}
				return
			}
			requirePermanent(t, err)
			_ = s.Send(context.Background(), data, nil)
			if d.renders > 1 {
				t.Fatal("failure redrawn")
			}
		})
	}
}
func TestAppImageCancellationBeforeCommitNeverDraws(t *testing.T) {
	d := newAppImageDevice()
	s := appSender(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	err := s.Send(ctx, testPNG(t, color.Black), func(int, int) { cancel() })
	if !errors.Is(err, context.Canceled) || d.commits != 0 || d.renders != 0 {
		t.Fatal("canceled receive committed", err)
	}
}
func TestAppImageLateNativeCompletionKeepsIdentity(t *testing.T) {
	d := newAppImageDevice()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	d.afterCommit = func() { close(entered); <-release }
	s := appSender(t, d)
	var runs atomic.Int32
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		runs.Add(1)
		c := d.client(t)
		done := make(chan error, 1)
		go func() { done <- job(ctx, c); close(finished) }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	data := testPNG(t, color.Black)
	done := make(chan error, 1)
	go func() { done <- s.Send(ctx, data, nil) }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	close(release)
	<-finished
	// Canceled native operation can finish the write but skip the signed read.
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		return job(ctx, d.client(t))
	}
	if err := s.Send(context.Background(), data, nil); err != nil || d.renders != 1 || d.commits != 1 {
		t.Fatal(err)
	}
}
func TestAppImageRejectsInvalidInputWithoutRadio(t *testing.T) {
	d := newAppImageDevice()
	s := appSender(t, d)
	requirePermanent(t, s.Send(context.Background(), []byte("bad png"), nil))
	if d.connections != 0 {
		t.Fatal("invalid PNG used radio")
	}
	s.withClient = func(context.Context, string, string, func(context.Context, *Client) error) error { return nil }
	requirePermanent(t, s.Send(context.Background(), testPNG(t, color.Black), nil))
}
