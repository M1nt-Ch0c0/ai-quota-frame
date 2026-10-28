package ble

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"io"
	"sync"
	"time"
)

const MaxImageSize = 5 * 1024 * 1024
const ImageTimeout = 90 * time.Minute

var ErrImageOutcomeUnknown = errors.New("BLE image outcome is unknown; inspect the display before explicitly sending again")
var ErrImagePending = errors.New("BLE image completion is pending; retry will query its receipt, not repaint")
var ErrImageUnsupported = errors.New("host does not support BLE images; update the host firmware")

// Permanent prevents automatic repeats of invalid, rejected or ambiguous images.
// The transport keeps receipt identity even if cancellation races native I/O.
type imageError struct {
	err            error
	permanent      bool
	retryOnRefresh bool
}

func (e *imageError) Error() string        { return e.err.Error() }
func (e *imageError) Unwrap() error        { return e.err }
func (e *imageError) Permanent() bool      { return e.permanent }
func (e *imageError) RetryOnRefresh() bool { return e.retryOnRefresh }
func imageFailure(err error) error         { return &imageError{err: err, permanent: true} }
func imageStateFailure(err error) error {
	return &imageError{err: err, permanent: true, retryOnRefresh: true}
}

// ValidatePNG performs the same dimensions/palette/envelope checks expected by
// the device. The device independently decodes before any display I/O.
func ValidatePNG(data []byte) error {
	if len(data) < 33 || len(data) > MaxImageSize || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return errors.New("PNG must be nonempty and at most 5 MiB")
	}
	if string(data[12:16]) != "IHDR" || binary.BigEndian.Uint32(data[8:12]) != 13 ||
		binary.BigEndian.Uint32(data[16:20]) != 800 || binary.BigEndian.Uint32(data[20:24]) != 480 || data[28] != 0 {
		return errors.New("PNG must be 800x480 and non-interlaced")
	}
	ended := false
	for offset := 8; offset < len(data); {
		if len(data)-offset < 12 {
			return errors.New("truncated PNG chunk")
		}
		n := uint64(binary.BigEndian.Uint32(data[offset:]))
		if n+12 > uint64(len(data)-offset) {
			return errors.New("truncated PNG chunk")
		}
		next := offset + int(n) + 12
		if string(data[offset+4:offset+8]) == "IEND" {
			if n != 0 || next != len(data) {
				return errors.New("PNG contains invalid or trailing data")
			}
			ended = true
		}
		offset = next
	}
	if !ended {
		return errors.New("PNG has no end chunk")
	}
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid PNG encoding or checksum")
	}
	for y := 0; y < 480; y++ {
		for x := 0; x < 800; x++ {
			r, g, b, a := im.At(x, y).RGBA()
			if a != 65535 {
				return errors.New("PNG must be completely opaque")
			}
			switch [3]uint32{r, g, b} {
			case [3]uint32{0, 0, 0}, [3]uint32{65535, 65535, 65535}, [3]uint32{65535, 65535, 0},
				[3]uint32{65535, 0, 0}, [3]uint32{0, 0, 65535}, [3]uint32{0, 65535, 0}:
			default:
				return errors.New("PNG contains colors outside the six-color palette")
			}
		}
	}
	return nil
}

type imageTransaction struct {
	id, boot      [16]byte
	digest        [32]byte
	size          uint32
	commitStarted bool
	terminal      bool
	result        error
}

// ImageSender is reused by the serialized pusher. Keeping it across retries and
// snapshot changes prevents a lost COMMIT response from creating a new draw job
// for the same image. Receipt state is in RAM and is not a cross-process ledger.
type ImageSender struct {
	id, token, app string
	mu             sync.Mutex
	current        *imageTransaction
	withClient     func(context.Context, string, string, func(context.Context, *Client) error) error
	random         io.Reader
}

func NewImageSender(id, token, app string) (*ImageSender, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return nil, err
	}
	if err = validateToken(token); err != nil {
		return nil, err
	}
	if _, err = encodeApp(app); err != nil {
		return nil, err
	}
	return &ImageSender{id: id, token: token, app: app, withClient: WithClient, random: rand.Reader}, nil
}

func (s *ImageSender) finish(tx *imageTransaction, result error) error {
	s.mu.Lock()
	tx.terminal, tx.result = true, result
	s.mu.Unlock()
	return result
}

// Send returns nil only after an authenticated physical-completion result.
// A retry after an uncertain COMMIT only queries the original receipt. A changed
// device boot or an evicted receipt permanently rejects that ambiguous image.
func (s *ImageSender) Send(ctx context.Context, data []byte, progress func(int, int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidatePNG(data); err != nil {
		return imageFailure(err)
	}
	digest := sha256.Sum256(data)
	s.mu.Lock()
	tx := s.current
	if tx == nil || tx.digest != digest {
		tx = &imageTransaction{digest: digest, size: uint32(len(data))}
		if _, err := io.ReadFull(s.random, tx.id[:]); err != nil {
			s.mu.Unlock()
			return imageFailure(errors.New("generate image transaction ID"))
		}
		if tx.id == [16]byte{} {
			s.mu.Unlock()
			return imageFailure(errors.New("invalid image transaction ID"))
		}
		s.current = tx
	}
	terminal, outcome := tx.terminal, tx.result
	s.mu.Unlock()
	if terminal {
		return outcome
	}
	// Native calls can finish after context cancellation returns to the caller.
	// Own the bytes until that worker actually leaves the radio operation.
	data = bytes.Clone(data)
	operation, cancel := context.WithTimeout(ctx, ImageTimeout)
	defer cancel()
	err := s.withClient(operation, s.id, s.token, func(ctx context.Context, c *Client) error {
		caps, err := c.command(ctx, OpImageCaps, nil)
		if err != nil {
			var code DeviceError
			if errors.As(err, &code) && code == 4 {
				return imageFailure(ErrImageUnsupported)
			}
			return err
		}
		if len(caps) != 22 || caps[0] != 1 || caps[21] == 0 || binary.LittleEndian.Uint32(caps[17:21]) < uint32(len(data)) {
			return imageFailure(errors.New("invalid or insufficient BLE image capabilities"))
		}
		s.mu.Lock()
		committed := tx.commitStarted
		if !committed {
			copy(tx.boot[:], caps[1:17])
		}
		boot, id := tx.boot, tx.id
		s.mu.Unlock()
		if committed {
			if !bytes.Equal(boot[:], caps[1:17]) {
				return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
			}
			query := append(bytes.Clone(boot[:]), id[:]...)
			receipt, err := c.command(ctx, OpImageResult, query)
			if err != nil {
				var code DeviceError
				if errors.As(err, &code) && code == 14 {
					return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
				}
				return err
			}
			if len(receipt) != 71 || receipt[0] == 0 {
				return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
			}
			target, _ := encodeApp(s.app)
			if binary.LittleEndian.Uint32(receipt[3:7]) != tx.size || !bytes.Equal(receipt[7:39], tx.digest[:]) || !bytes.Equal(receipt[39:], target) {
				return s.finish(tx, imageFailure(errors.New("BLE image receipt does not match the request")))
			}
			if receipt[0] == 1 {
				return ErrImagePending
			}
			if receipt[0] != 2 {
				return s.finish(tx, imageFailure(errors.New("invalid BLE image receipt state")))
			}
			if result := binary.LittleEndian.Uint16(receipt[1:3]); result != 0 {
				return s.finish(tx, imageFailure(DeviceError(result)))
			}
			return s.finish(tx, nil)
		}
		if link, ok := c.link.(interface{ preferThroughput() }); ok {
			link.preferThroughput()
		}
		begin := make([]byte, 100)
		copy(begin, boot[:])
		copy(begin[16:], id[:])
		binary.LittleEndian.PutUint32(begin[32:], tx.size)
		copy(begin[36:], tx.digest[:])
		target, _ := encodeApp(s.app)
		copy(begin[68:], target)
		ack, err := c.command(ctx, OpImageBegin, begin)
		if err != nil {
			return err
		}
		if len(ack) != 7 || binary.LittleEndian.Uint32(ack[:4]) > tx.size {
			return imageFailure(errors.New("invalid BLE image begin response"))
		}
		if ack[4] == 1 || ack[4] == 2 {
			s.mu.Lock()
			tx.commitStarted = true
			s.mu.Unlock()
			if ack[4] == 1 {
				return ErrImagePending
			}
			if result := binary.LittleEndian.Uint16(ack[5:]); result != 0 {
				return s.finish(tx, imageFailure(DeviceError(result)))
			}
			return s.finish(tx, nil)
		}
		if ack[4] != 0 {
			return imageFailure(errors.New("invalid BLE image receive state"))
		}
		if err := c.uploadChunks(ctx, data, int(binary.LittleEndian.Uint32(ack[:4])), progress); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Set before entering native I/O: cancellation can return to the caller
		// while the OS write still reaches the device. Never blindly resend.
		s.mu.Lock()
		tx.commitStarted = true
		s.mu.Unlock()
		_, err = c.command(ctx, OpCommit, nil)
		if err == nil {
			return s.finish(tx, nil)
		}
		var code DeviceError
		if errors.As(err, &code) {
			return s.finish(tx, imageFailure(err))
		}
		return err
	})
	s.mu.Lock()
	terminal, outcome, committed := tx.terminal, tx.result, tx.commitStarted
	s.mu.Unlock()
	if terminal {
		return outcome
	}
	if errors.Is(err, ErrAuthentication) {
		return imageFailure(err)
	}
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return err
	}
	if committed {
		if errors.Is(err, ErrImagePending) {
			return ErrImagePending
		}
		return fmt.Errorf("%w: %v", ErrImagePending, err)
	}
	if err == nil {
		return imageFailure(errors.New("BLE physical completion was not confirmed"))
	}
	return err
}

// Push waits for a receipt after a lost COMMIT reply. It never automatically
// retransmits an image whose physical outcome is ambiguous.
func (s *ImageSender) Push(ctx context.Context, data []byte, progress func(int, int)) error {
	for {
		err := s.Send(ctx, data, progress)
		if !errors.Is(err, ErrImagePending) {
			return err
		}
		if err = wait(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}
