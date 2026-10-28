// Package ble implements the PhotoPainter BLE v1 transport. The wire format is
// shared with photopainter-host/docs/ble-protocol.md. It transports PNG images
// and application packages; host firmware and partition tables are never written.
package ble

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
)

const (
	OpAuth byte = iota + 1
	OpInfo
	OpStatus
	OpBegin
	OpChunk
	OpCommit
	OpAbort
	OpProgress
	OpActivate
	OpSwitch
	OpRollback
	OpRemove
	OpAppRead
	OpAppWrite
	OpImageCaps
	OpImageBegin
	OpImageResult
)

const MaxPackageSize = 1024 * 1024

var ErrAuthentication = errors.New("BLE response authentication failed; check the saved pairing and reconnect")

type DeviceInfo struct {
	Version  byte       `json:"version"`
	State    byte       `json:"state"`
	ID       string     `json:"id"`
	IP       netip.Addr `json:"ip"`
	SSIDHash string     `json:"ssid_hash"`
}

func ParseInfo(p []byte) (DeviceInfo, error) {
	if len(p) != 20 || (p[0] != 1 && p[0] != 2) || p[1] > 3 {
		return DeviceInfo{}, errors.New("unsupported BLE discovery record")
	}
	return DeviceInfo{p[0], p[1], hex.EncodeToString(p[2:8]), netip.AddrFrom4([4]byte(p[8:12])), hex.EncodeToString(p[12:20])}, nil
}

func NormalizeID(id string) (string, error) {
	id = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), ":", ""))
	p, err := hex.DecodeString(id)
	if err != nil || len(p) != 6 {
		return "", errors.New("device ID must contain 12 hexadecimal digits")
	}
	return id, nil
}

// Link must serialize GATT exchanges and enforce the caller's context. A failed
// exchange makes the session unusable: reconnect and retransmit the whole package.
type Link interface {
	Info(context.Context) ([]byte, error)
	Challenge(context.Context) ([]byte, error)
	Exchange(context.Context, []byte) ([]byte, error)
}

type Client struct {
	link       Link
	key        []byte
	seq        uint32
	dead       bool
	info       DeviceInfo
	cocPSM     uint16
	cocVersion uint32
	bindingID  string
}

func tag(key []byte, domain string, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(domain))
	h.Write(data)
	return h.Sum(nil)
}

func Authenticate(ctx context.Context, link Link, id, token string) (*Client, error) {
	return authenticate(ctx, link, id, token, rand.Reader)
}

func validateToken(token string) error {
	if len(token) < 32 || len(token) > 128 {
		return errors.New("PHOTOFRAME_PUSH_TOKEN must be 32..128 printable ASCII bytes")
	}
	for i := range token {
		if token[i] < 0x21 || token[i] > 0x7e {
			return errors.New("PHOTOFRAME_PUSH_TOKEN contains invalid bytes")
		}
	}
	return nil
}

func authenticate(ctx context.Context, link Link, id, token string, random io.Reader) (*Client, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return nil, err
	}
	if err := validateToken(token); err != nil {
		return nil, err
	}
	record, err := link.Info(ctx)
	if err != nil {
		return nil, err
	}
	info, err := ParseInfo(record)
	if err != nil || info.ID != id {
		return nil, errors.New("BLE device ID does not match the selected device")
	}
	if info.Version != 1 {
		return nil, ErrNotPaired // v2 never negotiates a fixed token or downgrades.
	}
	nonce, err := link.Challenge(ctx)
	if err != nil || len(nonce) != 32 {
		return nil, errors.New("BLE challenge unavailable; reconnect")
	}
	clientNonce := make([]byte, 32)
	if _, err := io.ReadFull(random, clientNonce); err != nil {
		return nil, errors.New("generate BLE client nonce")
	}
	transcript := append(append(append([]byte{}, record[2:8]...), nonce...), clientNonce...)
	proof := tag([]byte(token), "pp-ble-v1/client", transcript)
	request := append(append([]byte{OpAuth}, clientNonce...), proof...)
	client := &Client{link: link, key: tag([]byte(token), "pp-ble-v1/session", transcript)}
	response, err := link.Exchange(ctx, request)
	if err != nil {
		return nil, err
	}
	data, err := client.verify(OpAuth, 0, response)
	if err != nil {
		return nil, err
	}
	client.info, err = ParseInfo(data)
	if err != nil || client.info.ID != id {
		return nil, errors.New("authenticated BLE device identity mismatch")
	}
	return client, nil
}

type DeviceError uint16

func (e DeviceError) Error() string {
	names := [...]string{"ok", "unauthorized", "token unconfigured", "busy", "invalid request", "unexpected offset", "SHA-256 mismatch", "out of memory", "invalid application state", "invalid package", "storage or application failure", "session expired", "invalid PNG image", "physical display failed", "device boot changed"}
	if int(e) < len(names) {
		return "PhotoPainter BLE: " + names[e]
	}
	return fmt.Sprintf("PhotoPainter BLE error %d", e)
}

func (c *Client) verify(op byte, seq uint32, p []byte) ([]byte, error) {
	if len(p) < 39 || len(p) > 512 || p[0] != op || binary.LittleEndian.Uint32(p[1:5]) != seq ||
		!hmac.Equal(tag(c.key, "pp-ble-v1/response", p[:len(p)-32]), p[len(p)-32:]) {
		c.dead = true
		return nil, ErrAuthentication
	}
	if code := binary.LittleEndian.Uint16(p[5:7]); code != 0 {
		return nil, DeviceError(code)
	}
	return p[7 : len(p)-32], nil
}

func (c *Client) command(ctx context.Context, op byte, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.dead || c.seq == ^uint32(0) || len(payload) > 475 {
		return nil, errors.New("BLE session unavailable; reconnect")
	}
	c.seq++
	p := make([]byte, 5, 5+len(payload)+32)
	p[0] = op
	binary.LittleEndian.PutUint32(p[1:], c.seq)
	p = append(p, payload...)
	p = append(p, tag(c.key, "pp-ble-v1/request", p)...)
	response, err := c.link.Exchange(ctx, p)
	if err != nil {
		c.dead = true
		return nil, err
	}
	return c.verify(op, c.seq, response)
}

func (c *Client) Info() DeviceInfo { return c.info }

func (c *Client) BindingID() string { return c.bindingID }

// PushURL is only available after mutual authentication. Advertisements alone
// must never select a destination that will receive the HTTP Bearer token.
func (c *Client) PushURL() (string, error) {
	if c.info.State != 2 || !c.info.IP.Is4() || !c.info.IP.IsPrivate() {
		return "", errors.New("device has no reachable private IPv4; BLE management remains available")
	}
	return "http://" + c.info.IP.String() + "/api/push", nil
}

func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	var result []byte
	var total uint32
	for {
		p := make([]byte, 4)
		binary.LittleEndian.PutUint32(p, uint32(len(result)))
		data, err := c.command(ctx, OpStatus, p)
		if err != nil {
			return nil, err
		}
		if len(data) <= 8 {
			return nil, errors.New("truncated BLE status page")
		}
		n := binary.LittleEndian.Uint32(data[:4])
		if n > 4095 || n == 0 || total != 0 && total != n || binary.LittleEndian.Uint32(data[4:8]) != uint32(len(result)) {
			return nil, errors.New("inconsistent BLE status pages")
		}
		total = n
		result = append(result, data[8:]...)
		if len(result) > int(total) {
			return nil, errors.New("oversize BLE status")
		}
		if len(result) == int(total) {
			if !json.Valid(result) {
				return nil, errors.New("invalid BLE status JSON")
			}
			return result, nil
		}
	}
}

func encodeApp(id string) ([]byte, error) {
	if len(id) < 1 || len(id) > 31 {
		return nil, errors.New("application ID must be 1..31 characters")
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil, errors.New("invalid application ID")
		}
	}
	p := make([]byte, 32)
	copy(p, id)
	return p, nil
}

type Package struct {
	App     string `json:"app"`
	Version uint32 `json:"version"`
	ABI     uint32 `json:"abi"`
	Bytes   int    `json:"bytes"`
}

// PackageTransferError exposes a bounded phase name while preserving the
// underlying rejection/timeout for callers. It never contains package bytes.
type PackageTransferError struct {
	Stage string
	Err   error
}

func (e *PackageTransferError) Error() string { return "BLE package " + e.Stage + ": " + e.Err.Error() }
func (e *PackageTransferError) Unwrap() error { return e.Err }

// InspectPackage validates the existing tools/module.py envelope and digest.
// The host remains authoritative for ELF relocation/manifest/import validation.
func InspectPackage(p []byte) (Package, error) {
	if len(p) < 4096+52 || len(p) > MaxPackageSize || binary.LittleEndian.Uint32(p[:4]) != 0x31464c45 ||
		binary.LittleEndian.Uint32(p[12:16]) != uint32(len(p)-4096) {
		return Package{}, errors.New("invalid PhotoPainter package size or magic")
	}
	format, abi := binary.LittleEndian.Uint32(p[4:8]), binary.LittleEndian.Uint32(p[8:12])
	if !(format == 1 && abi == 1 || format == 2 && (abi == 1 || abi == 2)) {
		return Package{}, errors.New("unsupported PhotoPainter package format or ABI")
	}
	id := "photoframe"
	if format == 2 {
		id = strings.TrimRight(string(p[52:84]), "\x00")
		encoded, err := encodeApp(id)
		if err != nil || !hmac.Equal(encoded, p[52:84]) {
			return Package{}, errors.New("invalid package application ID")
		}
	}
	digest := sha256.Sum256(p[4096:])
	if !hmac.Equal(digest[:], p[20:52]) {
		return Package{}, errors.New("package payload SHA-256 mismatch")
	}
	elf := p[4096:]
	if string(elf[:7]) != "\x7fELF\x01\x01\x01" || binary.LittleEndian.Uint16(elf[16:18]) != 3 || binary.LittleEndian.Uint16(elf[18:20]) != 94 {
		return Package{}, errors.New("package payload must be an ELF32 little-endian Xtensa ET_DYN")
	}
	return Package{id, binary.LittleEndian.Uint32(p[16:20]), abi, len(p)}, nil
}

// Install stages only. Activation is deliberately a separate explicit command.
func (c *Client) Install(ctx context.Context, p []byte, progress func(int, int)) error {
	metadata, err := InspectPackage(p)
	if err != nil {
		return err
	}
	// A throughput hint is useful only for a sustained authenticated transfer.
	// Unsupported adapters retain their defaults; correctness is unchanged.
	if link, ok := c.link.(interface{ preferThroughput() }); ok {
		link.preferThroughput()
	}
	id, _ := encodeApp(metadata.App)
	digest := sha256.Sum256(p)
	begin := make([]byte, 68)
	binary.LittleEndian.PutUint32(begin, uint32(len(p)))
	copy(begin[4:], digest[:])
	copy(begin[36:], id)
	var ack []byte
	deadline := time.Now().Add(2 * time.Second)
	for {
		ack, err = c.command(ctx, OpBegin, begin)
		var rejected DeviceError
		// Signed BUSY proves BEGIN did not acquire admission or allocate an
		// upload. Retry only that explicit rejection, never transport errors,
		// DATA, COMMIT, or any response whose authentication is uncertain.
		if !errors.As(err, &rejected) || rejected != 3 || !time.Now().Before(deadline) {
			break
		}
		delay := min(75*time.Millisecond, time.Until(deadline))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return &PackageTransferError{Stage: "begin", Err: ctx.Err()}
		case <-timer.C:
		}
	}
	if err != nil {
		return &PackageTransferError{Stage: "begin", Err: err}
	}
	if len(ack) != 4 || binary.LittleEndian.Uint32(ack) > uint32(len(p)) {
		return &PackageTransferError{Stage: "begin", Err: errors.New("invalid BLE upload offset")}
	}
	if err = c.uploadChunks(ctx, p, int(binary.LittleEndian.Uint32(ack)), progress); err != nil {
		return &PackageTransferError{Stage: "data", Err: err}
	}
	_, err = c.command(ctx, OpCommit, nil)
	if err != nil {
		return &PackageTransferError{Stage: "commit", Err: err}
	}
	return nil
}

func (c *Client) uploadChunks(ctx context.Context, p []byte, offset int, progress func(int, int)) error {
	if offset < 0 || offset > len(p) {
		return errors.New("invalid BLE upload offset")
	}
	for offset < len(p) {
		end := min(offset+400, len(p))
		chunk := make([]byte, 4, 4+end-offset)
		binary.LittleEndian.PutUint32(chunk, uint32(offset))
		chunk = append(chunk, p[offset:end]...)
		ack, err := c.command(ctx, OpChunk, chunk)
		if err != nil {
			return err
		}
		if len(ack) != 4 || binary.LittleEndian.Uint32(ack) != uint32(end) {
			return errors.New("invalid BLE chunk acknowledgement")
		}
		offset = end
		if progress != nil {
			progress(offset, len(p))
		}
	}
	return nil
}

func (c *Client) Manage(ctx context.Context, op byte, app string) error {
	if op < OpActivate || op > OpRemove {
		return errors.New("invalid application operation")
	}
	id, err := encodeApp(app)
	if err != nil {
		return err
	}
	_, err = c.command(ctx, op, id)
	return err
}

func (c *Client) appTarget(ctx context.Context, app string, characteristic uint32) ([]byte, error) {
	if _, err := encodeApp(app); err != nil {
		return nil, err
	}
	data, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	var state struct {
		Generation uint32 `json:"generation"`
		Revision   uint32 `json:"ble_service_revision"`
		Count      uint32 `json:"ble_characteristics"`
		Loaded     int    `json:"loaded_app"`
		Apps       []struct {
			ID   string `json:"id"`
			Bank int    `json:"bank"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, errors.New("invalid application BLE status")
	}
	matched := false
	for _, entry := range state.Apps {
		if entry.Bank == state.Loaded && entry.ID == app {
			matched = true
		}
	}
	if !matched || state.Generation == 0 || state.Revision == 0 || characteristic >= state.Count {
		return nil, errors.New("requested application's BLE service is not active")
	}
	target := make([]byte, 12)
	binary.LittleEndian.PutUint32(target, state.Generation)
	binary.LittleEndian.PutUint32(target[4:], state.Revision)
	binary.LittleEndian.PutUint32(target[8:], characteristic)
	return target, nil
}

func (c *Client) AppRead(ctx context.Context, app string, characteristic uint32) ([]byte, error) {
	target, err := c.appTarget(ctx, app, characteristic)
	if err != nil {
		return nil, err
	}
	return c.command(ctx, OpAppRead, target)
}

// AppWrite waits for the App Manager to deliver and complete the write event.
// Read a readable characteristic separately; write-only characteristics work too.
func (c *Client) AppWrite(ctx context.Context, app string, characteristic uint32, value []byte) error {
	if len(value) > 128 {
		return errors.New("application BLE values are limited to 128 bytes")
	}
	target, err := c.appTarget(ctx, app, characteristic)
	if err != nil {
		return err
	}
	_, err = c.command(ctx, OpAppWrite, append(target, value...))
	return err
}
