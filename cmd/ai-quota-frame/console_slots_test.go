package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

func slotFixture() *console.Device {
	var d console.Device
	_ = json.Unmarshal([]byte(`{"loaded_app":1,"loaded":1,"version":4,"ready":true,"payload_abi":2,"runtime_ready":true,"confirmed":true,"trial_app":-1,"apps":[{"id":"photoframe","bank":0,"active":0,"previous":-1,"pending":-1,"versions":[15,18],"abi":1,"button_eligible":false},{"id":"color-test","bank":1,"active":1,"previous":0,"pending":-1,"versions":[1,4],"abi":2,"button_eligible":true}]}`), &d)
	return &d
}
func slotRequest(slot int, version uint32) console.Request {
	return console.Request{Action: "use-slot", App: "photoframe", Slot: &slot, Version: &version}
}

type slotClient struct {
	states          []*console.Device
	reads, installs int
	ops             []byte
	apps            []string
}

func (c *slotClient) Status(context.Context) (json.RawMessage, error) {
	i := c.reads
	c.reads++
	if i >= len(c.states) || c.states[i] == nil {
		return nil, errors.New("read timeout")
	}
	p, err := json.Marshal(c.states[i])
	return p, err
}
func (c *slotClient) Manage(_ context.Context, op byte, app string) error {
	c.ops = append(c.ops, op)
	c.apps = append(c.apps, app)
	return errors.New("response lost after write")
}
func (c *slotClient) Install(context.Context, []byte, func(int, int)) error {
	c.installs++
	return errors.New("response lost after upload")
}
func slotBridge(t *testing.T, states ...*console.Device) (*consoleBridge, *slotClient) {
	t.Helper()
	b := newConsoleBridge(console.NewState(time.Minute, "", "", "", t.TempDir()), "", "", func(bool) {}, nil)
	c := &slotClient{states: states}
	b.withClient = func(ctx context.Context, work func(context.Context, consoleDeviceClient) error) error {
		return work(ctx, c)
	}
	return b, c
}
func loadedPhoto(abi int, version uint32, slot int) *console.Device {
	d := slotFixture()
	d.LoadedApp = 0
	d.Loaded = slot
	d.Version = version
	d.ABI = abi
	d.Apps[0].Active = slot
	d.Apps[0].ABI = abi
	if abi == 1 {
		d.Ready = false
		d.Confirmed = false
		d.TrialApp = 0
	}
	return d
}

func TestConsoleManualSwitchAllowsLegacyAndVerifiesLoadedSlot(t *testing.T) {
	before, after := slotFixture(), loadedPhoto(1, 15, 0)
	b, c := slotBridge(t, before, after)
	if err := b.selectSlot(context.Background(), slotRequest(0, 15)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpSwitch}) {
		t.Fatal(c.ops)
	}
	if b.state.Snapshot().Device.Ready {
		t.Fatal("legacy was falsely marked runtime-ready")
	}
}
func TestConsoleSlotSelectionRejectsStaleVersionBeforeMutation(t *testing.T) {
	b, c := slotBridge(t, slotFixture())
	if err := b.selectSlot(context.Background(), slotRequest(1, 17)); err == nil {
		t.Fatal("stale version accepted")
	}
	if len(c.ops) != 0 || c.installs != 0 {
		t.Fatal("stale choice changed device")
	}
}
func TestConsoleActivationRecoversLostReplyWithoutRepeating(t *testing.T) {
	before, after := slotFixture(), loadedPhoto(2, 18, 1)
	before.Apps[0].Pending = 1
	after.Apps[0].Previous = 0
	b, c := slotBridge(t, before, after)
	if err := b.selectSlot(context.Background(), slotRequest(1, 18)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpActivate}) {
		t.Fatal(c.ops)
	}
}
func TestConsoleInactiveAppRollbackThenSwitch(t *testing.T) {
	before, catalog, after := slotFixture(), slotFixture(), loadedPhoto(2, 18, 1)
	before.Apps[0].Previous = 1
	catalog.Apps[0].Active = 1
	catalog.Apps[0].Previous = 0
	b, c := slotBridge(t, before, catalog, after)
	if err := b.selectSlot(context.Background(), slotRequest(1, 18)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpRollback, ble.OpSwitch}) {
		t.Fatal(c.ops)
	}
}
func TestConsoleRollbackDoesNotSwitchWithoutVerifiedCatalog(t *testing.T) {
	before := slotFixture()
	before.Apps[0].Previous = 1
	b, c := slotBridge(t, before, before)
	if err := b.selectSlot(context.Background(), slotRequest(1, 18)); err == nil {
		t.Fatal("unapplied rollback accepted")
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpRollback}) {
		t.Fatal(c.ops)
	}
}
func TestConsoleUncertainManagementNeverRepeatsMutation(t *testing.T) {
	before := slotFixture()
	before.Apps[0].Pending = 1
	b, c := slotBridge(t, before, nil, nil)
	if err := b.selectSlot(context.Background(), slotRequest(1, 18)); err == nil {
		t.Fatal("unknown outcome accepted")
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpActivate}) {
		t.Fatal(c.ops)
	}
}
func consoleSlotPackage(t *testing.T, version uint32) (string, string) {
	t.Helper()
	p := make([]byte, 4096+52)
	binary.LittleEndian.PutUint32(p, 0x31464c45)
	binary.LittleEndian.PutUint32(p[4:], 2)
	binary.LittleEndian.PutUint32(p[8:], 2)
	binary.LittleEndian.PutUint32(p[12:], 52)
	binary.LittleEndian.PutUint32(p[16:], version)
	copy(p[52:], "photoframe")
	copy(p[4096:], "\x7fELF\x01\x01\x01")
	binary.LittleEndian.PutUint16(p[4112:], 3)
	binary.LittleEndian.PutUint16(p[4114:], 94)
	hash := sha256.Sum256(p[4096:])
	copy(p[20:], hash[:])
	path := filepath.Join(t.TempDir(), "app.pkg")
	if err := os.WriteFile(path, p, 0600); err != nil {
		t.Fatal(err)
	}
	_, m, err := readConsolePackage(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, m.Digest
}
func TestConsoleRestoresUntrackedSpareOnlyAfterVerifiedStaging(t *testing.T) {
	for _, stagedOK := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "not_staged"}[stagedOK], func(t *testing.T) {
			before, staged, after := slotFixture(), slotFixture(), loadedPhoto(2, 18, 1)
			if stagedOK {
				staged.Apps[0].Pending = 1
			}
			b, c := slotBridge(t, before, staged, after)
			r := slotRequest(1, 18)
			r.Path, r.Digest = consoleSlotPackage(t, 18)
			err := b.selectSlot(context.Background(), r)
			if (err == nil) != stagedOK {
				t.Fatal(err)
			}
			if c.installs != 1 {
				t.Fatal("upload was repeated", c.installs)
			}
			if stagedOK && !reflect.DeepEqual(c.ops, []byte{ble.OpActivate}) {
				t.Fatal(c.ops)
			}
			if !stagedOK && len(c.ops) != 0 {
				t.Fatal("activated unverified package")
			}
		})
	}
}
func TestConsoleSpareRestoreRejectsUnreviewedOrDifferentPackage(t *testing.T) {
	for _, bad := range []string{"missing", "digest", "version"} {
		t.Run(bad, func(t *testing.T) {
			b, c := slotBridge(t, slotFixture())
			r := slotRequest(1, 18)
			if bad != "missing" {
				v := uint32(18)
				if bad == "version" {
					v = 19
				}
				r.Path, r.Digest = consoleSlotPackage(t, v)
			}
			if bad == "digest" {
				r.Digest = "wrong"
			}
			if err := b.selectSlot(context.Background(), r); err == nil {
				t.Fatal("invalid restore accepted")
			}
			if c.installs != 0 || len(c.ops) != 0 {
				t.Fatal("invalid restore changed device")
			}
		})
	}
}
func TestConsoleRejectsEmptySlotAndAllowsVersionZero(t *testing.T) {
	d := slotFixture()
	d.Apps[0].Versions[0] = nil
	if _, _, _, _, err := planConsoleSlot(slotRequest(0, 0), d); err == nil {
		t.Fatal("empty slot accepted")
	}
	zero := uint32(0)
	d.Apps[0].Versions[0] = &zero
	if _, _, _, op, err := planConsoleSlot(slotRequest(0, 0), d); err != nil || op != ble.OpSwitch {
		t.Fatal(op, err)
	}
}
func TestConsoleCancelLegacyTrialRestoresOriginalApplication(t *testing.T) {
	b, c := slotBridge(t, loadedPhoto(1, 15, 0), slotFixture())
	if err := b.cancelTrial(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ops, []byte{ble.OpRollback}) || c.apps[0] != "photoframe" {
		t.Fatal(c.ops, c.apps)
	}
}
func TestConsoleRechecksUncertainTransferAfterQueueWait(t *testing.T) {
	b, c := slotBridge(t, slotFixture())
	b.state.Unknown()
	if err := b.selectSlot(context.Background(), slotRequest(0, 15)); err == nil {
		t.Fatal("uncertain transfer allowed mutation")
	}
	if c.reads != 0 || len(c.ops) != 0 {
		t.Fatal("device accessed after uncertain transfer")
	}
}
