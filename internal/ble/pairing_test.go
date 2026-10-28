package ble

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pairingVector(t *testing.T) map[string]string {
	t.Helper()
	p, err := os.ReadFile("testdata/pairing-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err = json.Unmarshal(p, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func vectorBytes(t *testing.T, v map[string]string, key string) []byte {
	t.Helper()
	p, err := hex.DecodeString(v[key])
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPairingOpenSSLGoldenVectorMatchesGo(t *testing.T) {
	v := pairingVector(t)
	get := func(k string) []byte { return vectorBytes(t, v, k) }
	assert := func(name string, actual []byte) {
		t.Helper()
		if !bytes.Equal(actual, get(name)) {
			t.Fatalf("golden %s mismatch", name)
		}
	}
	d, cid, bid, n := get("deviceID"), get("clientID"), get("bindingID"), []byte(v["name"])
	private, err := ecdh.P256().NewPrivateKey(get("clientPrivate"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := ecdh.P256().NewPublicKey(get("serverPublic"))
	if err != nil {
		t.Fatal(err)
	}
	assert("clientPublic", private.PublicKey().Bytes())
	ccommit := pairClientCommit(d, cid, bid, get("clientPublic"), get("clientNonce"), n)
	assert("clientCommit", ccommit)
	scommit := pairServerCommit(d, cid, bid, ccommit, get("serverPublic"), get("serverNonce"), n)
	assert("serverCommit", scommit)
	transcript := pairTranscript(d, cid, bid, ccommit, scommit, get("clientPublic"), get("clientNonce"), get("serverPublic"), get("serverNonce"), n)
	assert("transcript", transcript)
	shared, err := private.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	assert("shared", shared)
	assert("salt", shaDomain("pp-pair-v2/salt", transcript))
	root := pairRoot(shared, transcript)
	assert("root", root)
	if code := pairCode(root, transcript); code != v["code"] {
		t.Fatal("SAS mismatch", code)
	}
	poll := join([]byte{2}, d, cid, bid, get("challenge"))
	assert("pollTranscript", poll)
	assert("pollProof", tag(root, "pp-pair-v2/poll", poll))
	assert("cancelProof", tag(root, "pp-pair-v2/cancel", poll))
	a := bondTranscript(d, cid, bid, get("challenge"), get("authNonce"))
	assert("authTranscript", a)
	assert("authProof", tag(root, "pp-bond-v2/client", a))
	assert("session", tag(root, "pp-bond-v2/session", a))
}

type fakePairHost struct {
	t                                                          *testing.T
	info, challenge, private, nonce                            []byte
	cid, bid, ccommit, spub, scommit, cpub, cnonce, name, root []byte
	state                                                      PairingState
	approve, owner, dropApproval, tamperCommit                 bool
	commits, reveals, polls, approvals, auths                  int
	beforeReveal                                               func()
}

func newPairHost(t *testing.T) *fakePairHost {
	t.Helper()
	v := pairingVector(t)
	return &fakePairHost{t: t, info: append([]byte{2, 0}, append(vectorBytes(t, v, "deviceID"), make([]byte, 12)...)...), challenge: vectorBytes(t, v, "challenge"), private: vectorBytes(t, v, "serverPrivate"), nonce: vectorBytes(t, v, "serverNonce"), state: PairUnbound}
}
func (f *fakePairHost) Info(context.Context) ([]byte, error) { return bytes.Clone(f.info), nil }
func (f *fakePairHost) Challenge(context.Context) ([]byte, error) {
	return bytes.Clone(f.challenge), nil
}
func pairReply(op byte, code uint16, payload []byte, key []byte, domain string) []byte {
	header := make([]byte, 7)
	header[0] = op
	binary.LittleEndian.PutUint16(header[5:], code)
	result := append(header, payload...)
	if key != nil {
		result = append(result, tag(key, domain, result)...)
	}
	return result
}
func (f *fakePairHost) stateReply(op byte, code uint16) []byte {
	p := join([]byte{2}, f.bid, []byte{byte(f.state)}, make([]byte, 4), f.challenge)
	binary.LittleEndian.PutUint32(p[18:22], 120000)
	return pairReply(op, code, p, f.root, "pp-pair-v2/response")
}
func (f *fakePairHost) Exchange(_ context.Context, p []byte) ([]byte, error) {
	op := p[0]
	switch op {
	case OpPairInfo:
		bid, cid := f.bid, f.cid
		if len(bid) == 0 {
			bid = f.challenge[:16]
		}
		if len(cid) == 0 {
			cid = make([]byte, 16)
		}
		payload := join([]byte{2, byte(f.state)}, bid, cid, make([]byte, 4), []byte{byte(len(f.name))}, f.name)
		return pairReply(op, 0, payload, nil, ""), nil
	case OpPairCommit:
		f.commits++
		if f.owner {
			return pairReply(op, 8, nil, nil, ""), nil
		}
		if len(p) < 66 || len(p) != 66+int(p[65]) || !bytes.Equal(p[17:33], f.challenge[:16]) {
			f.t.Fatal("invalid commit wire")
		}
		f.cid = bytes.Clone(p[1:17])
		f.bid = bytes.Clone(p[17:33])
		f.ccommit = bytes.Clone(p[33:65])
		f.name = bytes.Clone(p[66:])
		private, _ := ecdh.P256().NewPrivateKey(f.private)
		f.spub = private.PublicKey().Bytes()
		f.scommit = pairServerCommit(f.info[2:8], f.cid, f.bid, f.ccommit, f.spub, f.nonce, f.name)
		f.state = PairWaitingReveal
		commit := bytes.Clone(f.scommit)
		if f.tamperCommit {
			commit[0] ^= 1
		}
		return pairReply(op, 0, join([]byte{2}, f.bid, f.spub, commit), nil, ""), nil
	case OpPairReveal:
		f.reveals++
		if len(p) != 114 || !bytes.Equal(p[1:17], f.bid) {
			f.t.Fatal("invalid reveal wire")
		}
		f.cpub = bytes.Clone(p[17:82])
		f.cnonce = bytes.Clone(p[82:114])
		if !bytes.Equal(pairClientCommit(f.info[2:8], f.cid, f.bid, f.cpub, f.cnonce, f.name), f.ccommit) {
			f.t.Fatal("client commitment mismatch")
		}
		private, _ := ecdh.P256().NewPrivateKey(f.private)
		public, _ := ecdh.P256().NewPublicKey(f.cpub)
		shared, _ := private.ECDH(public)
		t := pairTranscript(f.info[2:8], f.cid, f.bid, f.ccommit, f.scommit, f.cpub, f.cnonce, f.spub, f.nonce, f.name)
		f.root = pairRoot(shared, t)
		f.state = PairWaitingReady
		payload := join([]byte{2}, f.bid, f.nonce, []byte{byte(f.state)}, make([]byte, 4), f.challenge)
		binary.LittleEndian.PutUint32(payload[50:54], 30000)
		if f.beforeReveal != nil {
			f.beforeReveal()
		}
		return pairReply(op, 0, payload, f.root, "pp-pair-v2/response"), nil
	case OpPairPoll, OpPairCancel:
		if len(p) != 49 || !bytes.Equal(p[1:17], f.bid) {
			f.t.Fatal("invalid poll wire")
		}
		domain := "pp-pair-v2/poll"
		if op == OpPairCancel {
			domain = "pp-pair-v2/cancel"
		}
		if !hmac.Equal(p[17:], tag(f.root, domain, join([]byte{2}, f.info[2:8], f.cid, f.bid, f.challenge))) {
			f.t.Fatal("invalid proof")
		}
		if op == OpPairCancel {
			f.state = PairCancelled
			return f.stateReply(op, 0), nil
		}
		f.polls++
		stored, err := LoadBond(hex.EncodeToString(f.info[2:8]))
		if err != nil || !bytes.Equal(stored.secret, f.root) {
			f.t.Fatal("device armed before durable candidate", err)
		}
		if f.approve && !f.owner {
			f.owner = true
			f.approvals++
			f.state = PairBound
			if f.dropApproval {
				f.dropApproval = false
				return nil, errors.New("simulated lost physical approval reply")
			}
		}
		if !f.owner {
			f.state = PairAwaitingButton
		}
		return f.stateReply(op, 0), nil
	case OpBondAuth:
		f.auths++
		if len(p) != 97 || !f.owner || !bytes.Equal(p[1:17], f.cid) || !bytes.Equal(p[17:33], f.bid) {
			return pairReply(op, 1, nil, nil, ""), nil
		}
		a := bondTranscript(f.info[2:8], f.cid, f.bid, f.challenge, p[33:65])
		if !hmac.Equal(p[65:], tag(f.root, "pp-bond-v2/client", a)) {
			return pairReply(op, 1, nil, nil, ""), nil
		}
		return pairReply(op, 0, f.info, tag(f.root, "pp-bond-v2/session", a), "pp-ble-v1/response"), nil
	default:
		f.t.Fatalf("unexpected operation %d", op)
		return nil, errors.New("unexpected operation")
	}
}
func pairingTestStorage(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PHOTOFRAME_BLE_BOND_DIR", filepath.Join(dir, "bonds"))
	t.Setenv("PHOTOFRAME_BLE_BINDING_FILE", filepath.Join(dir, "selected.json"))
}
func TestPhysicalPairingLostApprovalRecoversWithoutSecondCeremony(t *testing.T) {
	pairingTestStorage(t)
	f := newPairHost(t)
	f.dropApproval = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := PairOnLink(ctx, f, "000102030405", "Chrome on test Mac", func(p PairingProgress) {
		if p.State == PairAwaitingButton {
			if len(p.Code) != 6 {
				t.Fatal("no comparison code")
			}
			f.approve = true
		}
	})
	if !errors.Is(err, ErrPairingPending) {
		t.Fatal("lost approval not preserved", err)
	}
	stored, err := LoadBond("000102030405")
	if err != nil || !stored.Pending {
		t.Fatal("candidate lost", err)
	}
	f.challenge = bytes.Repeat([]byte{0x92}, 32)
	client, err := PairOnLink(ctx, f, "000102030405", "Chrome on test Mac", nil)
	if err != nil || client.BindingID() != stored.BindingID {
		t.Fatal("resume failed", err)
	}
	if f.commits != 1 || f.reveals != 1 || f.approvals != 1 || f.auths != 1 {
		t.Fatal("pairing ceremony repeated")
	}
	stored, err = LoadBond("000102030405")
	if err != nil || stored.Pending {
		t.Fatal("binding not confirmed", err)
	}
	f.owner = false
	f.root = nil
	f.state = PairUnbound
	f.challenge = bytes.Repeat([]byte{0x93}, 32)
	if _, err = AuthenticateBond(ctx, f, *stored); !errors.Is(err, ErrAuthentication) {
		t.Fatal("old key accepted after physical reset", err)
	}
	if f.commits != 1 {
		t.Fatal("reset silently started another pairing")
	}
}
func TestPairingStorageFailureAndCommitmentTamperNeverArmDevice(t *testing.T) {
	for _, mode := range []string{"storage", "commitment"} {
		t.Run(mode, func(t *testing.T) {
			pairingTestStorage(t)
			f := newPairHost(t)
			if mode == "commitment" {
				f.tamperCommit = true
			} else {
				f.beforeReveal = func() {
					directory, _ := bondDirectory()
					if err := os.WriteFile(directory, []byte("block directory creation"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := PairOnLink(ctx, f, "000102030405", "Test client", nil); err == nil {
				t.Fatal("invalid pairing succeeded")
			}
			if f.polls != 0 || f.owner {
				t.Fatal("invalid pairing armed physical confirmation")
			}
		})
	}
}
