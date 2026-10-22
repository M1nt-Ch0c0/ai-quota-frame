package ble

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

type fakeLink struct {
	info, nonce, key     []byte
	requests             [][]byte
	alter                bool
	sequence             uint32
	packageData          []byte
	total                uint32
	digest               []byte
	committed, activated bool
	fail                 byte
	appStatus            []byte
	appValue             []byte
	appGeneration        uint32
	handle               func(byte, []byte) ([]byte, DeviceError, bool, error)
}

func newFake() *fakeLink {
	f := &fakeLink{info: []byte{1, 2, 0, 1, 2, 3, 4, 5, 192, 168, 1, 42, 0, 0, 0, 0, 0, 0, 0, 0}, nonce: make([]byte, 32)}
	for i := range f.nonce {
		f.nonce[i] = byte(i)
	}
	return f
}
func (f *fakeLink) Info(context.Context) ([]byte, error)      { return bytes.Clone(f.info), nil }
func (f *fakeLink) Challenge(context.Context) ([]byte, error) { return bytes.Clone(f.nonce), nil }
func (f *fakeLink) Exchange(_ context.Context, p []byte) ([]byte, error) {
	f.requests = append(f.requests, bytes.Clone(p))
	if p[0] == f.fail {
		return nil, errors.New("simulated link loss")
	}
	var data []byte
	var code DeviceError
	if p[0] == OpAuth {
		transcript := append(append(bytes.Clone(f.info[2:8]), f.nonce...), p[1:33]...)
		if !hmac.Equal(tag([]byte(testToken), "pp-ble-v1/client", transcript), p[33:]) {
			return []byte{1, 0, 0, 0, 0, 1, 0}, nil
		}
		f.key = tag([]byte(testToken), "pp-ble-v1/session", transcript)
		data = bytes.Clone(f.info)
	} else {
		if !hmac.Equal(tag(f.key, "pp-ble-v1/request", p[:len(p)-32]), p[len(p)-32:]) {
			return nil, errors.New("bad client MAC")
		}
		seq := binary.LittleEndian.Uint32(p[1:5])
		if seq != f.sequence+1 {
			return nil, errors.New("replay")
		}
		f.sequence = seq
		payload := p[5 : len(p)-32]
		handled := false
		if f.handle != nil {
			var err error
			data, code, handled, err = f.handle(p[0], payload)
			if err != nil {
				return nil, err
			}
		}
		if !handled {
			switch p[0] {
			case OpStatus:
				status := []byte(`{"apps":[],"phase":"` + strings.Repeat("x", 900) + `"}`)
				if f.appStatus != nil {
					status = f.appStatus
				}
				offset := int(binary.LittleEndian.Uint32(payload))
				end := min(offset+400, len(status))
				data = make([]byte, 8)
				binary.LittleEndian.PutUint32(data, uint32(len(status)))
				binary.LittleEndian.PutUint32(data[4:], uint32(offset))
				data = append(data, status[offset:end]...)
			case OpBegin:
				f.total = binary.LittleEndian.Uint32(payload)
				f.digest = bytes.Clone(payload[4:36])
				data = make([]byte, 4)
			case OpChunk:
				if binary.LittleEndian.Uint32(payload) != uint32(len(f.packageData)) {
					return nil, errors.New("wrong chunk offset")
				}
				f.packageData = append(f.packageData, payload[4:]...)
				data = make([]byte, 4)
				binary.LittleEndian.PutUint32(data, uint32(len(f.packageData)))
			case OpCommit:
				digest := sha256.Sum256(f.packageData)
				if uint32(len(f.packageData)) != f.total || !hmac.Equal(digest[:], f.digest) {
					return nil, errors.New("bad upload")
				}
				f.committed = true
			case OpActivate:
				f.activated = true
			case OpAppRead, OpAppWrite:
				if len(payload) < 12 || binary.LittleEndian.Uint32(payload) != f.appGeneration || binary.LittleEndian.Uint32(payload[4:]) != 9 || binary.LittleEndian.Uint32(payload[8:]) != 0 {
					return nil, errors.New("stale or wrong application service")
				}
				if p[0] == OpAppWrite {
					f.appValue = bytes.Clone(payload[12:])
				} else {
					data = bytes.Clone(f.appValue)
				}
			}
		}
	}
	out := make([]byte, 7)
	out[0] = p[0]
	binary.LittleEndian.PutUint32(out[1:], f.sequence)
	binary.LittleEndian.PutUint16(out[5:], uint16(code))
	out = append(out, data...)
	out = append(out, tag(f.key, "pp-ble-v1/response", out)...)
	if f.alter {
		out[len(out)-1] ^= 1
	}
	return out, nil
}

func TestApplicationBLEWriteReadAndGenerationBinding(t *testing.T) {
	f := newFake()
	f.appGeneration = 7
	f.appStatus = []byte(`{"generation":7,"ble_service_revision":9,"ble_characteristics":1,"loaded_app":2,"apps":[{"bank":2,"id":"ble-echo"}]}`)
	c := connectFake(t, f)
	ctx := context.Background()
	if err := c.AppWrite(ctx, "ble-echo", 0, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := c.AppRead(ctx, "ble-echo", 0)
	if err != nil || string(got) != "hello" {
		t.Fatalf("echo: %q, %v", got, err)
	}
	before := len(f.requests)
	if err := c.AppWrite(ctx, "ble-echo", 0, make([]byte, 129)); err == nil || len(f.requests) != before {
		t.Fatal("oversize app write sent")
	}
	if _, err := c.AppRead(ctx, "photoframe", 0); err == nil {
		t.Fatal("wrong app accepted")
	}
	if _, err := c.AppRead(ctx, "ble-echo", 1); err == nil {
		t.Fatal("wrong characteristic accepted")
	}
	f.appGeneration = 8
	if err := c.AppWrite(ctx, "ble-echo", 0, []byte("stale")); err == nil || string(f.appValue) != "hello" {
		t.Fatal("stale generation write applied")
	}
}
func connectFake(t *testing.T, f *fakeLink) *Client {
	t.Helper()
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i + 32)
	}
	c, err := authenticate(context.Background(), f, "000102030405", testToken, bytes.NewReader(random))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestSharedCHMACVectorAndDiscovery(t *testing.T) {
	f := newFake()
	c := connectFake(t, f)
	if hex.EncodeToString(f.requests[0][33:]) != "827d2691b1e2ac5990b86832bbaae8eaa53767b4a807d027d5a13c62cd9b495c" {
		t.Fatal("proof differs from C vector")
	}
	if hex.EncodeToString(c.key) != "2b1310093d3f4c44d104014ce803b48f19cfefc38a66f583daf21a5a3bd6aa2e" {
		t.Fatal("session differs from C vector")
	}
	url, err := c.PushURL()
	if err != nil || url != "http://192.168.1.42/api/push" {
		t.Fatalf("URL: %s, %v", url, err)
	}
	for _, p := range f.requests {
		if bytes.Contains(p, []byte(testToken)) {
			t.Fatal("token sent over BLE")
		}
	}
}
func TestRejectSpoofReplayWrongTokenAndMissingProof(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	f.alter = true
	if _, err := Authenticate(ctx, f, "000102030405", testToken); err == nil {
		t.Fatal("tampered device accepted")
	}
	f = newFake()
	if _, err := Authenticate(ctx, f, "000102030406", testToken); err == nil || len(f.requests) != 0 {
		t.Fatal("wrong ID accepted")
	}
	f = newFake()
	if _, err := Authenticate(ctx, f, "000102030405", strings.Repeat("z", 32)); err == nil {
		t.Fatal("wrong token accepted")
	}
	f = newFake()
	c := connectFake(t, f)
	if _, err := c.verify(OpStatus, 1, []byte{OpStatus, 1, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("unsigned success accepted")
	}
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("failed session reused")
	}
	f = newFake()
	c = connectFake(t, f)
	if _, err := c.verify(OpStatus, 1, append([]byte{OpStatus, 0, 0, 0, 0, 0, 0}, make([]byte, 32)...)); err == nil {
		t.Fatal("replay accepted")
	}
}
func makePackage() []byte {
	p := make([]byte, 5000)
	binary.LittleEndian.PutUint32(p, 0x31464c45)
	binary.LittleEndian.PutUint32(p[4:], 2)
	binary.LittleEndian.PutUint32(p[8:], 2)
	binary.LittleEndian.PutUint32(p[12:], uint32(len(p)-4096))
	binary.LittleEndian.PutUint32(p[16:], 7)
	copy(p[52:], "photoframe")
	copy(p[4096:], "\x7fELF\x01\x01\x01")
	binary.LittleEndian.PutUint16(p[4112:], 3)
	binary.LittleEndian.PutUint16(p[4114:], 94)
	digest := sha256.Sum256(p[4096:])
	copy(p[20:], digest[:])
	return p
}
func TestInstallOnlyStagesAndStatusPages(t *testing.T) {
	f := newFake()
	c := connectFake(t, f)
	p := makePackage()
	last := 0
	err := c.Install(context.Background(), p, func(done, total int) {
		if done <= last || total != len(p) {
			t.Fatal("invalid progress")
		}
		last = done
	})
	if err != nil || !f.committed || f.activated || !bytes.Equal(f.packageData, p) || last != len(p) {
		t.Fatalf("install: %v", err)
	}
	if err := c.Manage(context.Background(), OpActivate, "photoframe"); err != nil || !f.activated {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil || !bytes.Contains(status, []byte(`"apps":[]`)) {
		t.Fatalf("status %s %v", status, err)
	}
}
func TestPackageAndLossFailBeforeActivation(t *testing.T) {
	for _, mutate := range []func([]byte){func(p []byte) { p[0] ^= 1 }, func(p []byte) { p[4096] ^= 1 }, func(p []byte) { p[52] = '/' }, func(p []byte) { p[8] = 3 }, func(p []byte) { p[12] ^= 1 }} {
		p := makePackage()
		mutate(p)
		if _, err := InspectPackage(p); err == nil {
			t.Fatal("bad package accepted")
		}
	}
	f := newFake()
	c := connectFake(t, f)
	f.fail = OpChunk
	if err := c.Install(context.Background(), makePackage(), nil); err == nil || f.committed || f.activated {
		t.Fatal("loss was treated as successful install")
	}
	if err := c.Manage(context.Background(), OpActivate, "photoframe"); err == nil {
		t.Fatal("broken session reused")
	}
	f = newFake()
	c = connectFake(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Install(ctx, makePackage(), nil); !errors.Is(err, context.Canceled) || len(f.requests) != 1 {
		t.Fatal("cancelled upload sent data")
	}
}
func TestOfflineAndUnsafeDiscoveryNeverSelectHTTPDestination(t *testing.T) {
	for _, record := range [][]byte{
		{1, 0, 0, 1, 2, 3, 4, 5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{1, 2, 0, 1, 2, 3, 4, 5, 127, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0},
		{1, 2, 0, 1, 2, 3, 4, 5, 8, 8, 8, 8, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		f := newFake()
		f.info = record
		c := connectFake(t, f)
		if _, err := c.PushURL(); err == nil {
			t.Fatal("unsafe IP accepted")
		}
	}
}
