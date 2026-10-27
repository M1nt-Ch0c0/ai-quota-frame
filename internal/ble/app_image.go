package ble

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const appImageUUID = "a4fe200015364c88a2ce32ecdaac0471"
const (
	appImageBegin byte = iota + 1
	appImageChunk
	appImageCommit
	appImageCancel
	appImageQuery
)
const (
	appImageIdle byte = iota
	appImageReceiving
	appImageProcessing
	appImageComplete
	appImageFailed
	appImageCanceled
	appImageMissing
	appImageRejected
)

var ErrAppImageUnavailable = errors.New("photoframe BLE image service is unavailable; select an ABI 2.1 photoframe application")

type appImageReceipt struct {
	state, op      byte
	code           uint16
	id             [16]byte
	size, received uint32
	digest         [32]byte
}

func parseAppImageReceipt(p []byte) (appImageReceipt, error) {
	if len(p) != 64 || string(p[:4]) != "PFI1" || p[4] > appImageRejected {
		return appImageReceipt{}, imageFailure(errors.New("invalid photoframe image status"))
	}
	r := appImageReceipt{state: p[4], op: p[5], code: binary.LittleEndian.Uint16(p[6:]), size: binary.LittleEndian.Uint32(p[24:]), received: binary.LittleEndian.Uint32(p[28:])}
	copy(r.id[:], p[8:24])
	copy(r.digest[:], p[32:])
	return r, nil
}

func (c *Client) imageTarget(ctx context.Context, app string) ([]byte, error) {
	p, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	var state struct {
		Generation uint32 `json:"generation"`
		Revision   uint32 `json:"ble_service_revision"`
		Count      uint32 `json:"ble_characteristics"`
		UUID       string `json:"ble_service_uuid"`
		Loaded     int    `json:"loaded_app"`
		Ready      bool   `json:"runtime_ready"`
		CoCPSM     uint16 `json:"coc_psm"`
		CoCVersion uint32 `json:"coc_version"`
		Apps       []struct {
			ID   string `json:"id"`
			Bank int    `json:"bank"`
		} `json:"apps"`
	}
	if json.Unmarshal(p, &state) != nil {
		return nil, errors.New("invalid application BLE status")
	}
	matched := false
	for _, entry := range state.Apps {
		if entry.ID == app && entry.Bank == state.Loaded {
			matched = true
		}
	}
	if !matched || !state.Ready || state.Generation == 0 || state.Revision == 0 || state.Count != 2 || state.UUID != appImageUUID {
		return nil, ErrAppImageUnavailable
	}
	c.cocPSM, c.cocVersion = state.CoCPSM, state.CoCVersion
	target := make([]byte, 12)
	binary.LittleEndian.PutUint32(target, state.Generation)
	binary.LittleEndian.PutUint32(target[4:], state.Revision)
	return target, nil
}

func appImageWire(op byte, id [16]byte, tail []byte) []byte {
	p := make([]byte, 18, 18+len(tail))
	p[0], p[1] = 1, op
	copy(p[2:], id[:])
	return append(p, tail...)
}

// A signed shared-admission BUSY proves the command was not admitted. Timer
// events can briefly own admission between chunks. Retry only this explicit
// rejection, never a transport/authentication failure or ambiguous write.
func (c *Client) appImageCommand(ctx context.Context, op byte, p []byte) ([]byte, error) {
	for {
		value, err := c.command(ctx, op, p)
		var code DeviceError
		if !errors.As(err, &code) || code != 3 {
			return value, err
		}
		if err = wait(ctx, 50*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

// appImageExchange uses signed op 13/14, never advisory untagged GATT values.
// Target is frozen for the operation. Every command waits for the manager event.
func (c *Client) appImageExchange(ctx context.Context, target []byte, wire []byte) (appImageReceipt, error) {
	if len(wire) > 128 {
		return appImageReceipt{}, errors.New("photoframe command exceeds 128 bytes")
	}
	p := append(bytes.Clone(target), wire...)
	if _, err := c.appImageCommand(ctx, OpAppWrite, p); err != nil {
		return appImageReceipt{}, err
	}
	read := bytes.Clone(target)
	binary.LittleEndian.PutUint32(read[8:], 1)
	value, err := c.appImageCommand(ctx, OpAppRead, read)
	if err != nil {
		return appImageReceipt{}, err
	}
	r, err := parseAppImageReceipt(value)
	if err == nil && (r.op != wire[1] || !bytes.Equal(r.id[:], wire[2:18])) {
		err = imageFailure(errors.New("photoframe response does not match the command"))
	}
	return r, err
}

type appImageTransaction struct {
	id                  [16]byte
	digest              [32]byte
	size                uint32
	target              []byte
	committed, terminal bool
	result              error
}

// AppImageSender is the primary BLE PNG transport. The legacy host ImageSender
// remains available for its independent regression tests. No fallback or app
// switching occurs here. Ambiguous requests retain their identity across retries
// and snapshot replacement for the lifetime of this sender process.
type AppImageSender struct {
	id, token, app    string
	mu                sync.Mutex
	current           *appImageTransaction
	uncertain         map[[32]byte]*appImageTransaction
	blocked           map[[32]byte]error
	withClient        func(context.Context, string, string, func(context.Context, *Client) error) error
	random            io.Reader
	transportObserver func(string)
}

// SetTransportObserver reports the actual selected data path, after connection
// setup and before BEGIN. It never infers CoC from the device capability alone.
func (s *AppImageSender) SetTransportObserver(observer func(string)) {
	s.mu.Lock()
	s.transportObserver = observer
	s.mu.Unlock()
}
func (s *AppImageSender) reportTransport(name string) {
	s.mu.Lock()
	observer := s.transportObserver
	s.mu.Unlock()
	if observer != nil {
		observer(name)
	}
}

// PrepareExplicitPush creates a new user-requested display intent even when
// its PNG equals a previous successful frame. It never resets an uncertain
// transaction or clears an unknown-outcome block.
func (s *AppImageSender) PrepareExplicitPush(data []byte) error {
	digest := sha256.Sum256(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tx := range s.uncertain {
		if !tx.terminal {
			return ErrImagePending
		}
	}
	if err, ok := s.blocked[digest]; ok {
		return err
	}
	if s.current != nil && s.current.digest == digest && s.current.terminal && s.current.result == nil {
		s.current = nil
	}
	return nil
}

func NewAppImageSender(id, token, app string) (*AppImageSender, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return nil, err
	}
	if err = validateToken(token); err != nil {
		return nil, err
	}
	if app != "photoframe" {
		return nil, errors.New("BLE PNG input requires the photoframe application")
	}
	return &AppImageSender{id: id, token: token, app: app, withClient: WithClient, random: rand.Reader,
		uncertain: make(map[[32]byte]*appImageTransaction), blocked: make(map[[32]byte]error)}, nil
}

func (s *AppImageSender) finish(tx *appImageTransaction, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx.terminal, tx.result = true, err
	delete(s.uncertain, tx.digest)
	if err != nil {
		s.blocked[tx.digest] = err
	}
	return err
}

func appImageError(r appImageReceipt) error {
	names := []string{"ok", "invalid command", "busy", "unexpected offset", "conflicting request", "out of memory", "SHA-256 mismatch", "invalid PNG", "physical display failed", "receive timeout", "disconnected"}
	name := "unknown error"
	if int(r.code) < len(names) {
		name = names[r.code]
	}
	err := fmt.Errorf("photoframe BLE image: %s (%d)", name, r.code)
	if r.code == 2 || r.code == 5 {
		return err
	}
	return imageFailure(err)
}

func (s *AppImageSender) receipt(tx *appImageTransaction, r appImageReceipt) error {
	if r.state == appImageMissing {
		return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
	}
	if r.id != tx.id || r.digest != tx.digest || r.size != tx.size || r.received > r.size {
		return s.finish(tx, imageFailure(fmt.Errorf("%w: photoframe receipt metadata does not match the image", ErrImageOutcomeUnknown)))
	}
	if r.state == appImageProcessing && r.code == 0 && r.received == r.size {
		return ErrImagePending
	}
	if r.state == appImageComplete && r.code == 0 && r.received == r.size {
		return s.finish(tx, nil)
	}
	if r.state == appImageFailed || r.state == appImageCanceled {
		return s.finish(tx, imageFailure(appImageError(r)))
	}
	return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
}

func (s *AppImageSender) Send(ctx context.Context, data []byte, progress func(int, int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidatePNG(data); err != nil {
		return imageFailure(err)
	}
	digest := sha256.Sum256(data)
	s.mu.Lock()
	if err, ok := s.blocked[digest]; ok {
		s.mu.Unlock()
		return err
	}
	tx := s.uncertain[digest]
	if tx == nil && s.current != nil && s.current.digest == digest {
		tx = s.current
	}
	// A disconnected pre-COMMIT receive is canceled by the app. A new attempt
	// uses a new request ID; only a potentially committed request keeps its ID.
	if tx != nil && !tx.committed && !tx.terminal {
		tx = nil
	}
	if tx == nil {
		tx = &appImageTransaction{digest: digest, size: uint32(len(data))}
		if _, err := io.ReadFull(s.random, tx.id[:]); err != nil || tx.id == [16]byte{} {
			s.mu.Unlock()
			return imageFailure(errors.New("generate image request ID"))
		}
	}
	s.current = tx
	terminal, result := tx.terminal, tx.result
	s.mu.Unlock()
	if terminal {
		return result
	}
	data = bytes.Clone(data)
	operation, cancel := context.WithTimeout(ctx, ImageTimeout)
	defer cancel()
	err := s.withClient(operation, s.id, s.token, func(ctx context.Context, c *Client) error {
		target, err := c.imageTarget(ctx, s.app)
		s.mu.Lock()
		committed := tx.committed
		pinned := bytes.Clone(tx.target)
		s.mu.Unlock()
		if err != nil {
			if committed && errors.Is(err, ErrAppImageUnavailable) {
				return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
			}
			return err
		}
		if committed && !bytes.Equal(pinned, target) {
			return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
		}
		if opener, ok := c.link.(cocOpener); ok && opener.supportsCoC() && c.cocVersion == 1 && c.cocPSM != 0 {
			return s.sendCoC(ctx, c, target, tx, data, progress)
		}
		if committed {
			if !bytes.Equal(pinned, target) {
				return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
			}
			r, err := c.appImageExchange(ctx, target, appImageWire(appImageQuery, tx.id, nil))
			if err != nil {
				return err
			}
			return s.receipt(tx, r)
		}
		if link, ok := c.link.(interface{ preferThroughput() }); ok {
			link.preferThroughput()
		}
		s.reportTransport("gatt")
		begin := make([]byte, 36)
		binary.LittleEndian.PutUint32(begin, tx.size)
		copy(begin[4:], digest[:])
		r, err := c.appImageExchange(ctx, target, appImageWire(appImageBegin, tx.id, begin))
		if err != nil {
			return err
		}
		if r.state == appImageRejected {
			return appImageError(r)
		}
		if r.state == appImageComplete || r.state == appImageFailed || r.state == appImageCanceled {
			return s.receipt(tx, r)
		}
		if r.state != appImageReceiving || r.code != 0 || r.size != tx.size || r.digest != tx.digest || r.received > tx.size {
			return imageFailure(errors.New("invalid photoframe receive status"))
		}
		for offset := int(r.received); offset < len(data); {
			end := min(offset+106, len(data))
			tail := make([]byte, 4, 4+end-offset)
			binary.LittleEndian.PutUint32(tail, uint32(offset))
			tail = append(tail, data[offset:end]...)
			r, err = c.appImageExchange(ctx, target, appImageWire(appImageChunk, tx.id, tail))
			if err != nil {
				return err
			}
			if r.state == appImageRejected {
				return appImageError(r)
			}
			if r.state != appImageReceiving || r.code != 0 || r.size != tx.size || r.digest != tx.digest || r.received != uint32(end) {
				return imageFailure(errors.New("invalid photoframe chunk acknowledgement"))
			}
			offset = end
			if progress != nil {
				progress(offset, len(data))
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		// Set before entering native I/O, which can finish after caller cancellation.
		s.mu.Lock()
		tx.committed = true
		tx.target = bytes.Clone(target)
		s.uncertain[tx.digest] = tx
		s.mu.Unlock()
		r, err = c.appImageExchange(ctx, target, appImageWire(appImageCommit, tx.id, nil))
		if err != nil {
			return err
		}
		return s.receipt(tx, r)
	})
	s.mu.Lock()
	terminal, result, committed := tx.terminal, tx.result, tx.committed
	s.mu.Unlock()
	if terminal {
		return result
	}
	var permanent interface{ Permanent() bool }
	if errors.Is(err, ErrAuthentication) || (errors.As(err, &permanent) && permanent.Permanent()) {
		if committed {
			return s.finish(tx, imageFailure(ErrImageOutcomeUnknown))
		}
		return s.finish(tx, imageFailure(err))
	}
	if committed {
		return fmt.Errorf("%w: %v", ErrImagePending, err)
	}
	if err == nil {
		return imageFailure(errors.New("photoframe physical completion was not confirmed"))
	}
	return err
}

func (s *AppImageSender) Push(ctx context.Context, data []byte, progress func(int, int)) error {
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
