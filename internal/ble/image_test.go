package ble

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"
)

func testPNG(t *testing.T, ink color.Color) []byte {
	t.Helper()
	im := image.NewPaletted(image.Rect(0, 0, 800, 480), color.Palette{ink})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestValidateBLEImageContract(t *testing.T) {
	valid := testPNG(t, color.Black)
	if err := ValidatePNG(valid); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"short": []byte("png"), "trailing": append(bytes.Clone(valid), 0),
		"transparent": testPNG(t, color.Transparent), "off-palette": testPNG(t, color.RGBA{1, 2, 3, 255}),
		"oversize": make([]byte, MaxImageSize+1),
	} {
		t.Run(name, func(t *testing.T) {
			if ValidatePNG(data) == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-1] ^= 1
	if ValidatePNG(corrupt) == nil {
		t.Fatal("bad chunk checksum accepted")
	}
	interlaced := bytes.Clone(valid)
	interlaced[28] = 1
	if ValidatePNG(interlaced) == nil {
		t.Fatal("interlaced image accepted")
	}
	// A valid ignored ancillary chunk exercises the full existing 5 MiB limit.
	padding := make([]byte, MaxImageSize-len(valid))
	binary.BigEndian.PutUint32(padding, uint32(len(padding)-12))
	copy(padding[4:8], "vpAg")
	binary.BigEndian.PutUint32(padding[len(padding)-4:], crc32.ChecksumIEEE(padding[4:len(padding)-4]))
	maximum := append(append(bytes.Clone(valid[:len(valid)-12]), padding...), valid[len(valid)-12:]...)
	if len(maximum) != MaxImageSize || ValidatePNG(maximum) != nil {
		t.Fatal("maximum valid PNG rejected")
	}
}

type imageDevice struct {
	boot                                                   [16]byte
	receipts                                               map[[16]byte][]byte
	begins, chunks, commits, renders, queries, connections int
	dropCommit, dropChunk, unsupported, corruptReceipt     bool
	result                                                 DeviceError
	afterCommit                                            func()
}

func newImageDevice() *imageDevice {
	return &imageDevice{boot: [16]byte{42}, receipts: make(map[[16]byte][]byte)}
}

func (d *imageDevice) client(t *testing.T) *Client {
	t.Helper()
	d.connections++
	f := newFake()
	// No IP is available at any point in these tests.
	f.info[1] = 0
	clear(f.info[8:12])
	var begin, received []byte
	f.handle = func(op byte, payload []byte) ([]byte, DeviceError, bool, error) {
		switch op {
		case OpImageCaps:
			if d.unsupported {
				return nil, 4, true, nil
			}
			out := make([]byte, 22)
			out[0] = 1
			copy(out[1:], d.boot[:])
			binary.LittleEndian.PutUint32(out[17:], MaxImageSize)
			out[21] = 4
			return out, 0, true, nil
		case OpImageBegin:
			d.begins++
			if len(payload) != 100 || !bytes.Equal(payload[:16], d.boot[:]) {
				t.Fatal("bad begin frame")
			}
			begin = bytes.Clone(payload)
			received = nil
			return make([]byte, 7), 0, true, nil
		case OpChunk:
			d.chunks++
			if d.dropChunk {
				d.dropChunk = false
				return nil, 0, true, errors.New("receive link lost")
			}
			if binary.LittleEndian.Uint32(payload) != uint32(len(received)) {
				t.Fatal("chunk offset")
			}
			received = append(received, payload[4:]...)
			out := make([]byte, 4)
			binary.LittleEndian.PutUint32(out, uint32(len(received)))
			return out, 0, true, nil
		case OpCommit:
			d.commits++
			d.renders++
			if len(begin) != 100 || int(binary.LittleEndian.Uint32(begin[32:])) != len(received) {
				t.Fatal("incomplete image commit")
			}
			digest := sha256.Sum256(received)
			if !bytes.Equal(digest[:], begin[36:68]) {
				t.Fatal("image SHA mismatch")
			}
			var id [16]byte
			copy(id[:], begin[16:32])
			r := make([]byte, 71)
			r[0] = 2
			binary.LittleEndian.PutUint16(r[1:], uint16(d.result))
			copy(r[3:7], begin[32:36])
			copy(r[7:39], begin[36:68])
			copy(r[39:], begin[68:])
			d.receipts[id] = r
			if d.afterCommit != nil {
				d.afterCommit()
			}
			if d.dropCommit {
				d.dropCommit = false
				return nil, 0, true, errors.New("commit reply lost after refresh")
			}
			return nil, d.result, true, nil
		case OpImageResult:
			d.queries++
			if len(payload) != 32 || !bytes.Equal(payload[:16], d.boot[:]) {
				return nil, 14, true, nil
			}
			var id [16]byte
			copy(id[:], payload[16:])
			result := make([]byte, 71)
			copy(result, d.receipts[id])
			if d.corruptReceipt {
				result[7] ^= 1
			}
			return result, 0, true, nil
		}
		return nil, 0, false, nil
	}
	return connectFake(t, f)
}

func imageSender(t *testing.T, d *imageDevice) *ImageSender {
	t.Helper()
	s, err := NewImageSender("000102030405", testToken, "photoframe")
	if err != nil {
		t.Fatal(err)
	}
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		return job(ctx, d.client(t))
	}
	return s
}

func TestImageSendOfflineAndLostReplyRecoveryNeverRepaints(t *testing.T) {
	d := newImageDevice()
	d.dropCommit = true
	s := imageSender(t, d)
	data := testPNG(t, color.Black)
	if err := s.Send(context.Background(), data, nil); !errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	chunks := d.chunks
	if err := s.Send(context.Background(), data, nil); err != nil {
		t.Fatal(err)
	}
	if d.renders != 1 || d.commits != 1 || d.begins != 1 || d.chunks != chunks || d.queries != 1 {
		t.Fatalf("repainted: %+v", d)
	}
	connections := d.connections
	if err := s.Send(context.Background(), data, nil); err != nil || d.connections != connections {
		t.Fatal("completed image was retransmitted", err)
	}
	if err := s.Send(context.Background(), testPNG(t, color.White), nil); err != nil || d.renders != 2 {
		t.Fatal("changed image not rendered", err)
	}
}

func TestImageRetryBeforeCommitRetransmitsSafely(t *testing.T) {
	d := newImageDevice()
	d.dropChunk = true
	s := imageSender(t, d)
	data := testPNG(t, color.Black)
	if err := s.Send(context.Background(), data, nil); err == nil || errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	if d.renders != 0 {
		t.Fatal("partial receive displayed")
	}
	if err := s.Send(context.Background(), data, nil); err != nil || d.renders != 1 || d.begins != 2 {
		t.Fatal(err)
	}
}

func TestImageUnknownBootEvictionAndMismatchedReceiptDoNotRetryDisplay(t *testing.T) {
	for _, mode := range []string{"boot", "evicted", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			d := newImageDevice()
			d.dropCommit = true
			s := imageSender(t, d)
			data := testPNG(t, color.Black)
			if err := s.Send(context.Background(), data, nil); !errors.Is(err, ErrImagePending) {
				t.Fatal(err)
			}
			switch mode {
			case "boot":
				d.boot[0]++
			case "evicted":
				clear(d.receipts)
			case "mismatch":
				d.corruptReceipt = true
			}
			err := s.Send(context.Background(), data, nil)
			var permanent interface{ Permanent() bool }
			if err == nil || !errors.As(err, &permanent) || !permanent.Permanent() {
				t.Fatal("ambiguous image not stopped", err)
			}
			if mode != "mismatch" && !errors.Is(err, ErrImageOutcomeUnknown) {
				t.Fatal(err)
			}
			_ = s.Send(context.Background(), data, nil)
			if d.renders != 1 || d.commits != 1 || d.begins != 1 {
				t.Fatal("ambiguous commit repeated")
			}
		})
	}
}

func TestImageDisplayFailureAndUnsupportedHostArePermanent(t *testing.T) {
	for _, mode := range []string{"unsupported", "failed", "failed-receipt"} {
		t.Run(mode, func(t *testing.T) {
			d := newImageDevice()
			s := imageSender(t, d)
			if mode == "unsupported" {
				d.unsupported = true
			} else {
				d.result = 13
				d.dropCommit = mode == "failed-receipt"
			}
			data := testPNG(t, color.Black)
			err := s.Send(context.Background(), data, nil)
			if errors.Is(err, ErrImagePending) {
				err = s.Send(context.Background(), data, nil)
			}
			var permanent interface{ Permanent() bool }
			if err == nil || !errors.As(err, &permanent) || !permanent.Permanent() {
				t.Fatal(err)
			}
			if mode == "unsupported" && (d.begins != 0 || !errors.Is(err, ErrImageUnsupported)) {
				t.Fatal("sent to unsupported host")
			}
			_ = s.Send(context.Background(), data, nil)
			if d.renders > 1 {
				t.Fatal("failed physical refresh repeated")
			}
		})
	}
}

func TestImageCompletionAfterCallerCancellationKeepsReceiptIdentity(t *testing.T) {
	d := newImageDevice()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	d.afterCommit = func() { close(entered); <-release }
	s := imageSender(t, d)
	var runs atomic.Int32
	s.withClient = func(ctx context.Context, _, _ string, job func(context.Context, *Client) error) error {
		runs.Add(1)
		client := d.client(t)
		result := make(chan error, 1)
		go func() { result <- job(ctx, client); close(finished) }()
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	data := testPNG(t, color.Black)
	result := make(chan error, 1)
	go func() { result <- s.Send(ctx, data, nil) }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, ErrImagePending) {
		t.Fatal(err)
	}
	close(release)
	<-finished
	if err := s.Send(context.Background(), data, nil); err != nil || runs.Load() != 1 || d.renders != 1 {
		t.Fatal("late success lost", err)
	}
}

func TestImageRunnerCannotSucceedWithoutACompletionReceipt(t *testing.T) {
	s := imageSender(t, newImageDevice())
	s.withClient = func(context.Context, string, string, func(context.Context, *Client) error) error { return nil }
	if err := s.Send(context.Background(), testPNG(t, color.Black), nil); err == nil {
		t.Fatal("connection success counted as display success")
	}
}
