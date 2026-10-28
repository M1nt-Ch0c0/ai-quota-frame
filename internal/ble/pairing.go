package ble

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	OpPairInfo byte = 18 + iota
	OpPairCommit
	OpPairReveal
	OpPairPoll
	OpPairCancel
	OpBondAuth
)

type PairingState byte

const (
	PairUnbound PairingState = iota
	PairWaitingReveal
	PairWaitingReady
	PairDisplaying
	PairAwaitingButton
	PairBound
	PairCancelled
	PairExpired
	PairFault
)

const PairingTimeout = 15 * time.Minute

var (
	ErrPairingOwned     = errors.New("相框已与另一客户端配对，请在相框上长按按键重置配对")
	ErrPairingPending   = errors.New("配对结果尚待确认，请重新连接同一相框恢复配对")
	ErrPairingLegacy    = errors.New("此相框需要先更新支持按键配对的宿主固件")
	ErrPairingCancelled = errors.New("配对已取消，请重新发起")
	ErrPairingExpired   = errors.New("配对请求已过期，请重新发起")
)

// PairingInfo is public discovery metadata, not an authenticated identity.
type PairingInfo struct {
	State           PairingState `json:"state"`
	BindingID       string       `json:"binding_id"`
	ClientID        string       `json:"client_id"`
	RemainingMillis uint32       `json:"remaining_ms"`
	Name            string       `json:"name"`
}

type PairingProgress struct {
	State           PairingState `json:"state"`
	DeviceID        string       `json:"device_id"`
	BindingID       string       `json:"binding_id"`
	Code            string       `json:"code,omitempty"`
	RemainingMillis uint32       `json:"remaining_ms"`
}

func pairingName(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 32 {
		return nil, errors.New("pairing client name must contain 1..32 ASCII characters")
	}
	for _, b := range []byte(name) {
		if b < 0x20 || b > 0x7e {
			return nil, errors.New("pairing client name must use printable ASCII")
		}
	}
	return []byte(name), nil
}

func join(parts ...[]byte) []byte {
	var size int
	for _, p := range parts {
		size += len(p)
	}
	result := make([]byte, 0, size)
	for _, p := range parts {
		result = append(result, p...)
	}
	return result
}

func shaDomain(domain string, parts ...[]byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func pairClientCommit(d, cid, bid, pub, nonce, name []byte) []byte {
	return shaDomain("pp-pair-v2/client-commit", []byte{2}, d, cid, bid, pub, nonce, []byte{byte(len(name))}, name)
}
func pairServerCommit(d, cid, bid, clientCommit, pub, nonce, name []byte) []byte {
	return shaDomain("pp-pair-v2/server-commit", []byte{2}, d, cid, bid, clientCommit, pub, nonce, []byte{byte(len(name))}, name)
}
func pairTranscript(d, cid, bid, ccommit, scommit, cpub, cnonce, spub, snonce, name []byte) []byte {
	return join([]byte{2}, d, cid, bid, ccommit, scommit, cpub, cnonce, spub, snonce, []byte{byte(len(name))}, name)
}
func pairRoot(shared, transcript []byte) []byte {
	salt := shaDomain("pp-pair-v2/salt", transcript)
	prk := tag(salt, "", shared)
	return tag(prk, "pp-pair-v2/bond", []byte{1}) // HKDF-SHA256, one 32-byte block.
}
func pairCode(root, transcript []byte) string {
	return fmt.Sprintf("%06d", binary.LittleEndian.Uint32(tag(root, "pp-pair-v2/code", transcript))%1000000)
}
func bondTranscript(d, cid, bid, challenge, nonce []byte) []byte {
	return join([]byte{2}, d, cid, bid, challenge, nonce)
}

func bootstrapPayload(op byte, reply []byte) ([]byte, error) {
	if len(reply) < 7 || len(reply) > 512 || reply[0] != op || binary.LittleEndian.Uint32(reply[1:]) != 0 {
		return nil, errors.New("invalid pairing reply")
	}
	if code := binary.LittleEndian.Uint16(reply[5:]); code != 0 {
		return nil, DeviceError(code)
	}
	return reply[7:], nil
}

func ReadPairingInfo(ctx context.Context, link Link) (PairingInfo, error) {
	reply, err := link.Exchange(ctx, []byte{OpPairInfo})
	if err != nil {
		return PairingInfo{}, err
	}
	p, err := bootstrapPayload(OpPairInfo, reply)
	if err != nil {
		return PairingInfo{}, err
	}
	if len(p) < 39 || p[0] != 2 || p[1] > byte(PairFault) || int(p[38]) > 32 || len(p) != 39+int(p[38]) {
		return PairingInfo{}, errors.New("invalid pairing state")
	}
	name := string(p[39:])
	if len(name) != 0 {
		if _, err = pairingName(name); err != nil {
			return PairingInfo{}, err
		}
	}
	return PairingInfo{State: PairingState(p[1]), BindingID: hex.EncodeToString(p[2:18]), ClientID: hex.EncodeToString(p[18:34]), RemainingMillis: binary.LittleEndian.Uint32(p[34:38]), Name: name}, nil
}

func verifyPairState(op byte, bond Bond, challenge, reply []byte) (PairingProgress, error) {
	var status PairingProgress
	if len(reply) != 93 || reply[0] != op || binary.LittleEndian.Uint32(reply[1:]) != 0 ||
		!hmac.Equal(tag(bond.secret, "pp-pair-v2/response", reply[:61]), reply[61:]) {
		return status, ErrAuthentication
	}
	p := reply[7:61]
	bid, _ := hex.DecodeString(bond.BindingID)
	if p[0] != 2 || !hmac.Equal(p[1:17], bid) || p[17] > byte(PairFault) || !hmac.Equal(p[22:54], challenge) {
		return status, ErrAuthentication
	}
	status = PairingProgress{State: PairingState(p[17]), DeviceID: bond.DeviceID, BindingID: bond.BindingID, Code: bond.PairCode, RemainingMillis: binary.LittleEndian.Uint32(p[18:22])}
	if code := binary.LittleEndian.Uint16(reply[5:]); code != 0 {
		return status, DeviceError(code)
	}
	return status, nil
}

func pairingControl(ctx context.Context, link Link, op byte, bond Bond, challenge []byte) (PairingProgress, error) {
	d, _ := hex.DecodeString(bond.DeviceID)
	cid, _ := hex.DecodeString(bond.ClientID)
	bid, _ := hex.DecodeString(bond.BindingID)
	domain := "pp-pair-v2/poll"
	if op == OpPairCancel {
		domain = "pp-pair-v2/cancel"
	}
	proof := tag(bond.secret, domain, join([]byte{2}, d, cid, bid, challenge))
	reply, err := link.Exchange(ctx, join([]byte{op}, bid, proof))
	if err != nil {
		return PairingProgress{}, err
	}
	return verifyPairState(op, bond, challenge, reply)
}

func discardPendingBond(expected Bond) error {
	return withBondLock(expected.DeviceID, func() error { return discardLockedPendingBond(expected) })
}

func discardLockedPendingBond(expected Bond) error {
	current, err := readBond(expected.DeviceID, true)
	if err != nil {
		return err
	}
	if current == nil || !sameBond(*current, expected) {
		return errors.New("pairing candidate changed; not removed")
	}
	path, _ := bondPath(expected.DeviceID, true)
	if err := removePrivateBond(path); err != nil {
		return err
	}
	return nil
}

// PairOnLink runs the complete physical pairing ceremony on one serialized
// link. Candidate storage is committed before the first proof that arms the
// device display; a failed local write cannot produce an orphaned approval.
func PairOnLink(ctx context.Context, link Link, id, name string, progress func(PairingProgress)) (*Client, error) {
	return pairOnLink(ctx, link, id, name, progress, rand.Reader)
}

func pairOnLink(ctx context.Context, link Link, id, name string, progress func(PairingProgress), random io.Reader) (*Client, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return nil, err
	}
	n, err := pairingName(name)
	if err != nil {
		return nil, err
	}
	record, err := link.Info(ctx)
	if err != nil {
		return nil, err
	}
	info, err := ParseInfo(record)
	if err != nil || info.ID != id {
		return nil, ErrAuthentication
	}
	if info.Version != 2 {
		return nil, ErrPairingLegacy
	}
	challenge, err := link.Challenge(ctx)
	if err != nil || len(challenge) != 32 {
		return nil, ErrAuthentication
	}
	state, err := ReadPairingInfo(ctx, link)
	if err != nil {
		return nil, err
	}
	saved, savedErr := LoadBond(id)
	if savedErr != nil && !errors.Is(savedErr, ErrNotPaired) {
		return nil, savedErr
	}
	if saved != nil && state.BindingID == saved.BindingID && state.ClientID == saved.ClientID {
		if state.State == PairBound {
			client, err := AuthenticateBond(ctx, link, *saved)
			if err == nil && saved.Pending {
				err = confirmBond(*saved)
			}
			return client, err
		}
		if saved.Pending && ((state.State >= PairWaitingReady && state.State <= PairAwaitingButton) || state.State == PairCancelled || state.State == PairExpired) {
			return waitForPairing(ctx, link, *saved, challenge, progress)
		}
	}
	if state.State == PairBound {
		return nil, ErrPairingOwned
	}
	if state.State != PairUnbound && state.State != PairCancelled && state.State != PairExpired {
		return nil, ErrPairingPending
	}
	if saved != nil && saved.Pending {
		return nil, ErrPairingPending
	}
	bid := challenge[:16]
	if bytes.Equal(bid, make([]byte, 16)) || (state.State == PairUnbound && state.BindingID != hex.EncodeToString(bid)) {
		return nil, ErrAuthentication
	}
	cid := make([]byte, 16)
	cnonce := make([]byte, 32)
	if _, err = io.ReadFull(random, cid); err != nil {
		return nil, errors.New("cannot create pairing identity")
	}
	if bytes.Equal(cid, make([]byte, 16)) {
		return nil, errors.New("invalid random pairing identity")
	}
	private, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, errors.New("cannot create pairing key")
	}
	if _, err = io.ReadFull(random, cnonce); err != nil {
		return nil, errors.New("cannot create pairing nonce")
	}
	d := record[2:8]
	cpub := private.PublicKey().Bytes()
	ccommit := pairClientCommit(d, cid, bid, cpub, cnonce, n)
	reply, err := link.Exchange(ctx, join([]byte{OpPairCommit}, cid, bid, ccommit, []byte{byte(len(n))}, n))
	if err != nil {
		return nil, err
	}
	p, err := bootstrapPayload(OpPairCommit, reply)
	if err != nil {
		return nil, err
	}
	if len(p) != 114 || p[0] != 2 || !hmac.Equal(p[1:17], bid) {
		return nil, ErrAuthentication
	}
	spub := bytes.Clone(p[17:82])
	scommit := bytes.Clone(p[82:114])
	public, err := ecdh.P256().NewPublicKey(spub)
	if err != nil {
		return nil, ErrAuthentication
	}
	reveal, err := link.Exchange(ctx, join([]byte{OpPairReveal}, bid, cpub, cnonce))
	if err != nil {
		return nil, err
	}
	if len(reveal) != 125 || reveal[0] != OpPairReveal || binary.LittleEndian.Uint32(reveal[1:]) != 0 {
		return nil, ErrAuthentication
	}
	rp := reveal[7:93]
	if rp[0] != 2 || !hmac.Equal(rp[1:17], bid) || !hmac.Equal(rp[54:86], challenge) {
		return nil, ErrAuthentication
	}
	snonce := rp[17:49]
	if !hmac.Equal(pairServerCommit(d, cid, bid, ccommit, spub, snonce, n), scommit) {
		return nil, ErrAuthentication
	}
	transcript := pairTranscript(d, cid, bid, ccommit, scommit, cpub, cnonce, spub, snonce, n)
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, ErrAuthentication
	}
	root := pairRoot(shared, transcript)
	if !hmac.Equal(tag(root, "pp-pair-v2/response", reveal[:93]), reveal[93:]) {
		return nil, ErrAuthentication
	}
	if code := binary.LittleEndian.Uint16(reveal[5:]); code != 0 {
		return nil, DeviceError(code)
	}
	if PairingState(rp[49]) != PairWaitingReady {
		return nil, errors.New("device did not accept pairing preparation")
	}
	bond := Bond{DeviceID: id, ClientID: hex.EncodeToString(cid), BindingID: hex.EncodeToString(bid), Name: "PhotoPainter", PairCode: pairCode(root, transcript), CreatedAt: time.Now().UTC(), Pending: true, secret: root}
	if err = savePendingBond(bond); err != nil {
		return nil, err
	}
	return waitForPairing(ctx, link, bond, challenge, progress)
}

func waitForPairing(ctx context.Context, link Link, bond Bond, challenge []byte, progress func(PairingProgress)) (*Client, error) {
	for {
		status, err := pairingControl(ctx, link, OpPairPoll, bond, challenge)
		if err != nil {
			if status.State == PairCancelled || status.State == PairExpired {
				if removeErr := discardPendingBond(bond); removeErr != nil {
					return nil, removeErr
				}
				if status.State == PairExpired {
					return nil, ErrPairingExpired
				}
				return nil, ErrPairingCancelled
			}
			return nil, fmt.Errorf("%w: %v", ErrPairingPending, err)
		}
		if progress != nil && status.State != PairBound {
			progress(status)
		}
		switch status.State {
		case PairBound:
			client, err := AuthenticateBond(ctx, link, bond)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrPairingPending, err)
			}
			if err = confirmBond(bond); err != nil {
				return nil, err
			}
			if progress != nil {
				status.Code = ""
				progress(status)
			}
			return client, nil
		case PairWaitingReady, PairDisplaying, PairAwaitingButton:
		case PairCancelled, PairExpired:
			if err = discardPendingBond(bond); err != nil {
				return nil, err
			}
			if status.State == PairExpired {
				return nil, ErrPairingExpired
			}
			return nil, ErrPairingCancelled
		default:
			return nil, ErrPairingPending
		}
		if err = wait(ctx, 500*time.Millisecond); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPairingPending, err)
		}
	}
}

func AuthenticateBond(ctx context.Context, link Link, bond Bond) (*Client, error) {
	return authenticateBond(ctx, link, bond, rand.Reader)
}
func authenticateBond(ctx context.Context, link Link, bond Bond, random io.Reader) (*Client, error) {
	if err := validateBond(bond); err != nil {
		return nil, err
	}
	record, err := link.Info(ctx)
	if err != nil {
		return nil, err
	}
	info, err := ParseInfo(record)
	if err != nil || info.Version != 2 || info.ID != bond.DeviceID {
		return nil, ErrAuthentication
	}
	challenge, err := link.Challenge(ctx)
	if err != nil || len(challenge) != 32 {
		return nil, ErrAuthentication
	}
	nonce := make([]byte, 32)
	if _, err = io.ReadFull(random, nonce); err != nil {
		return nil, errors.New("cannot create authentication nonce")
	}
	cid, _ := hex.DecodeString(bond.ClientID)
	bid, _ := hex.DecodeString(bond.BindingID)
	a := bondTranscript(record[2:8], cid, bid, challenge, nonce)
	client := &Client{link: link, key: tag(bond.secret, "pp-bond-v2/session", a), bindingID: bond.BindingID}
	reply, err := link.Exchange(ctx, join([]byte{OpBondAuth}, cid, bid, nonce, tag(bond.secret, "pp-bond-v2/client", a)))
	if err != nil {
		return nil, err
	}
	p, err := client.verify(OpBondAuth, 0, reply)
	if err != nil {
		return nil, err
	}
	client.info, err = ParseInfo(p)
	if err != nil || client.info.Version != 2 || client.info.ID != bond.DeviceID {
		return nil, ErrAuthentication
	}
	return client, nil
}
