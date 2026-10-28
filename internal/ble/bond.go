package ble

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Bond is created by a physical pairing approval, never from a user-entered
// token. The secret is intentionally omitted from JSON/CLI diagnostic output.
type Bond struct {
	DeviceID  string    `json:"device_id"`
	ClientID  string    `json:"client_id"`
	BindingID string    `json:"binding_id"`
	Name      string    `json:"name"`
	PairCode  string    `json:"pair_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	PairedAt  time.Time `json:"paired_at,omitzero"`
	Pending   bool      `json:"pending"`
	secret    []byte
}

type pairingRequiredError struct{}

func (pairingRequiredError) Error() string {
	return "相框尚未与此客户端配对，请发起配对并在相框上按键确认"
}
func (pairingRequiredError) Permanent() bool { return true }

var ErrNotPaired error = pairingRequiredError{}

type storedBond struct {
	Version int    `json:"version"`
	Bond    Bond   `json:"bond"`
	Secret  []byte `json:"secret"`
}

func bondDirectory() (string, error) {
	if value := os.Getenv("PHOTOFRAME_BLE_BOND_DIR"); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("PHOTOFRAME_BLE_BOND_DIR must be absolute")
		}
		return value, nil
	}
	public, err := bindingPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(public), "ble-bonds"), nil
}

func bondPath(id string, pending bool) (string, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return "", err
	}
	directory, err := bondDirectory()
	if err != nil {
		return "", err
	}
	suffix := ".json"
	if pending {
		suffix = ".pending.json"
	}
	return filepath.Join(directory, id+suffix), nil
}

func validateBond(b Bond) error {
	id, err := NormalizeID(b.DeviceID)
	if err != nil || id != b.DeviceID || len(b.secret) != 32 || b.CreatedAt.IsZero() {
		return errors.New("invalid pairing record")
	}
	for _, value := range []string{b.ClientID, b.BindingID} {
		p, err := hex.DecodeString(value)
		if err != nil || len(p) != 16 || bytes.Equal(p, make([]byte, 16)) {
			return errors.New("invalid pairing identity")
		}
	}
	if !b.Pending && b.PairedAt.IsZero() {
		return errors.New("pairing record is not confirmed")
	}
	if b.Pending {
		if len(b.PairCode) != 6 {
			return errors.New("invalid pairing comparison code")
		}
		for _, c := range b.PairCode {
			if c < '0' || c > '9' {
				return errors.New("invalid pairing comparison code")
			}
		}
	}
	return nil
}

func readBond(id string, pending bool) (*Bond, error) {
	path, err := bondPath(id, pending)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		(runtime.GOOS != "windows" && before.Mode().Perm()&0077 != 0) {
		return nil, errors.New("pairing record must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open pairing record")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("pairing record changed while opening")
	}
	p, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(p) > 4096 {
		return nil, errors.New("invalid pairing record size")
	}
	decoder := json.NewDecoder(bytes.NewReader(p))
	decoder.DisallowUnknownFields()
	var stored storedBond
	var trailing any
	if decoder.Decode(&stored) != nil || decoder.Decode(&trailing) != io.EOF || stored.Version != 1 {
		return nil, errors.New("invalid pairing record format")
	}
	stored.Bond.secret = stored.Secret
	if stored.Bond.DeviceID != id || stored.Bond.Pending != pending || validateBond(stored.Bond) != nil {
		return nil, errors.New("pairing record does not match the selected device")
	}
	return &stored.Bond, nil
}

// LoadBond also recovers a durable candidate. The device may have committed
// physical approval before its final response or the local confirmation write
// was lost. A successful new mutual authentication confirms that candidate.
func LoadBond(id string) (*Bond, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return nil, err
	}
	if pending, err := readBond(id, true); err != nil || pending != nil {
		return pending, err
	}
	bond, err := readBond(id, false)
	if err == nil && bond == nil {
		return nil, ErrNotPaired
	}
	return bond, err
}

func writeBond(b Bond) error {
	if err := validateBond(b); err != nil {
		return err
	}
	path, err := bondPath(b.DeviceID, b.Pending)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create pairing directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pairing-")
	if err != nil {
		return errors.New("cannot create private pairing record")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		err = json.NewEncoder(f).Encode(storedBond{Version: 1, Bond: b, Secret: b.secret})
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = publishBondFile(name, path, b.Pending)
	}
	if err == nil && runtime.GOOS != "windows" {
		var directory *os.File
		directory, err = os.Open(filepath.Dir(path))
		if err == nil {
			err = directory.Sync()
			_ = directory.Close()
		}
	}
	if err != nil {
		return errors.New("cannot save pairing relationship")
	}
	return nil
}

func savePendingBond(b Bond) error {
	b.Pending = true
	b.PairedAt = time.Time{}
	return withBondLock(b.DeviceID, func() error { return writeBond(b) })
}

func confirmBond(b Bond) error {
	return withBondLock(b.DeviceID, func() error { return confirmLockedBond(b) })
}

func sameBond(a, b Bond) bool {
	return a.DeviceID == b.DeviceID && a.ClientID == b.ClientID && a.BindingID == b.BindingID && bytes.Equal(a.secret, b.secret)
}

func confirmLockedBond(b Bond) error {
	pending, err := readBond(b.DeviceID, true)
	if err != nil {
		return err
	}
	if pending == nil {
		// Confirmation may have committed the active record before a crash
		// or before publishing the public selection. Permit only that exact
		// already-confirmed relationship, never resurrect a forgotten one.
		active, err := readBond(b.DeviceID, false)
		if err != nil {
			return err
		}
		if active == nil || !sameBond(*active, b) {
			return errors.New("pairing candidate changed; confirmation not saved")
		}
		return SaveBinding(b.DeviceID)
	}
	if !sameBond(*pending, b) {
		return errors.New("pairing candidate changed; confirmation not saved")
	}
	b.Pending = false
	b.PairCode = ""
	if b.PairedAt.IsZero() {
		b.PairedAt = time.Now().UTC()
	}
	if err := writeBond(b); err != nil {
		return err
	}
	path, _ := bondPath(b.DeviceID, true)
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot finish local pairing confirmation")
	}
	return SaveBinding(b.DeviceID)
}

// ForgetBond only forgets this computer's credential. Revoking the device's
// owner is deliberately a physical button operation on the device.
func ForgetBond(id string) error {
	var err error
	id, err = NormalizeID(id)
	if err != nil {
		return err
	}
	return withBondLock(id, func() error { return forgetLockedBond(id) })
}

func forgetLockedBond(id string) error {
	for _, pending := range []bool{false, true} {
		path, err := bondPath(id, pending)
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot forget local pairing")
		}
	}
	selected, err := LoadBinding()
	if err != nil {
		return err
	}
	if selected == id {
		return RemoveBinding()
	}
	return nil
}

// Mutations are serialized per device across CLI and daemon processes. The
// lock file stays in place: unlinking it would allow two different inodes to
// claim the same logical lock while another process is still waiting.
func withBondLock(id string, work func() error) error {
	path, err := bondPath(id, false)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create pairing directory")
	}
	lockPath := path + ".lock"
	before, err := os.Lstat(lockPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect pairing record lock")
	}
	if before != nil && (!before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0) {
		return errors.New("pairing record lock must be a regular file")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("cannot open pairing record lock")
	}
	defer f.Close()
	opened, err := f.Stat()
	current, pathErr := os.Lstat(lockPath)
	if err != nil || pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) ||
		(runtime.GOOS != "windows" && opened.Mode().Perm()&0077 != 0) {
		return errors.New("pairing record lock changed or is not private")
	}
	unlock, err := lockBondFile(f)
	if err != nil {
		return errors.New("pairing record is in use; retry after the other operation completes")
	}
	defer unlock()
	return work()
}

func removePrivateBond(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove local pairing candidate")
	}
	return nil
}
