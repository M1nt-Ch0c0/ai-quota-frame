package ble

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testBond(t *testing.T) Bond {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("PHOTOFRAME_BLE_BOND_DIR", filepath.Join(directory, "bonds"))
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(directory, "selected.json"))
	return Bond{DeviceID: "aabbccddeeff", ClientID: "01010101010101010101010101010101", BindingID: "02020202020202020202020202020202", Name: "Test computer", PairCode: "123456", CreatedAt: time.Now().UTC(), Pending: true, secret: bytes.Repeat([]byte{0x53}, 32)}
}

func TestPairingCandidateNeverOverwritesAnotherRelationship(t *testing.T) {
	first := testBond(t)
	if err := savePendingBond(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.BindingID = "03030303030303030303030303030303"
	second.secret = bytes.Repeat([]byte{0x42}, 32)
	if err := savePendingBond(second); err == nil {
		t.Fatal("new candidate overwrote the existing relationship")
	}
	if err := confirmBond(second); err == nil {
		t.Fatal("unmatched candidate was promoted")
	}
	loaded, err := LoadBond(first.DeviceID)
	if err != nil || !sameBond(*loaded, first) || !loaded.Pending {
		t.Fatal("existing candidate was changed", err)
	}
	if err := discardPendingBond(first); err != nil {
		t.Fatal(err)
	}
	if err := savePendingBond(second); err != nil {
		t.Fatal(err)
	}
	if err := discardPendingBond(first); err == nil {
		t.Fatal("old cancellation removed a newer candidate")
	}
	loaded, err = LoadBond(first.DeviceID)
	if err != nil || !sameBond(*loaded, second) {
		t.Fatal("newer candidate was changed", err)
	}
}

func TestPairingConfirmationCannotResurrectForgottenRelationship(t *testing.T) {
	bond := testBond(t)
	if err := savePendingBond(bond); err != nil {
		t.Fatal(err)
	}
	if err := ForgetBond(bond.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err := confirmBond(bond); err == nil {
		t.Fatal("late confirmation resurrected a forgotten relationship")
	}
	if _, err := LoadBond(bond.DeviceID); !errors.Is(err, ErrNotPaired) {
		t.Fatal("forgotten relationship reappeared", err)
	}
}

func TestPairingConfirmationCanFinishSameCommittedRelationship(t *testing.T) {
	bond := testBond(t)
	if err := savePendingBond(bond); err != nil {
		t.Fatal(err)
	}
	if err := confirmBond(bond); err != nil {
		t.Fatal(err)
	}
	// The active credential may have committed before public selection or the
	// caller's success reply. Repeating only the local finalization is safe.
	if err := confirmBond(bond); err != nil {
		t.Fatal("could not finish the exact committed relationship", err)
	}
	other := bond
	other.secret = bytes.Repeat([]byte{0x44}, 32)
	if err := confirmBond(other); err == nil {
		t.Fatal("different secret replaced the committed relationship")
	}
}

func TestPairingMutationLockExcludesOtherNativeProcesses(t *testing.T) {
	const id = "aabbccddeeff"
	if os.Getenv("GO_TEST_BOND_LOCK_HELPER") == "1" {
		if err := withBondLock(id, func() error { return nil }); err == nil {
			t.Fatal("another native process acquired the occupied pairing lock")
		}
		return
	}
	_ = testBond(t)
	err := withBondLock(id, func() error {
		command := exec.Command(os.Args[0], "-test.run=^TestPairingMutationLockExcludesOtherNativeProcesses$")
		command.Env = append(os.Environ(), "GO_TEST_BOND_LOCK_HELPER=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("child lock check failed: %v\n%s", err, output)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := withBondLock(id, func() error { return nil }); err != nil {
		t.Fatal("lock was not released", err)
	}
}

func TestPairingCandidateSurvivesLostConfirmationWithoutExposingSecret(t *testing.T) {
	b := testBond(t)
	if _, err := LoadBond(b.DeviceID); !errors.Is(err, ErrNotPaired) {
		t.Fatal(err)
	}
	if err := savePendingBond(b); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBond(b.DeviceID)
	if err != nil || !loaded.Pending || !bytes.Equal(loaded.secret, b.secret) {
		t.Fatal("durable candidate unavailable", err)
	}
	public, _ := json.Marshal(loaded)
	if bytes.Contains(public, []byte("secret")) || bytes.Contains(public, []byte("U1NT")) {
		t.Fatal("diagnostic JSON exposed pairing key")
	}
	path, _ := bondPath(b.DeviceID, true)
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("pairing credential not private")
	}
	if err := confirmBond(*loaded); err != nil {
		t.Fatal(err)
	}
	confirmed, err := LoadBond(b.DeviceID)
	if err != nil || confirmed.Pending || confirmed.PairedAt.IsZero() {
		t.Fatal("pairing not promoted", err)
	}
	if selected, err := LoadBinding(); err != nil || selected != b.DeviceID {
		t.Fatal(selected, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("candidate not removed", err)
	}
	if err := ForgetBond(b.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBond(b.DeviceID); !errors.Is(err, ErrNotPaired) {
		t.Fatal(err)
	}
}

func TestPairingRecordRejectsCorruptionAndWorldReadableSecret(t *testing.T) {
	b := testBond(t)
	if err := savePendingBond(b); err != nil {
		t.Fatal(err)
	}
	path, _ := bondPath(b.DeviceID, true)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBond(b.DeviceID); err == nil {
			t.Fatal("world-readable secret accepted")
		}
		_ = os.Chmod(path, 0600)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"bond":{},"secret":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBond(b.DeviceID); err == nil {
		t.Fatal("corrupt bond accepted")
	}
}
