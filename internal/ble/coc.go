package ble

import (
	"context"
	"crypto/hmac"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

var ErrCoCUnsupported = errors.New("L2CAP CoC is unavailable on this client platform")

type cocStream interface {
	ReadContext(context.Context, []byte) (int, error)
	WriteContext(context.Context, []byte) (int, error)
	Close() error
}
type cocOpener interface {
	supportsCoC() bool
	openCoC(context.Context, uint16) (cocStream, error)
}
type cocSession struct {
	stream              cocStream
	sendKey, receiveKey []byte
	sendSeq, receiveSeq uint32
	sendMax, receiveMax int
}

func cocReadFull(ctx context.Context, stream cocStream, p []byte) error {
	for len(p) > 0 {
		n, err := stream.ReadContext(ctx, p)
		if n < 0 || n > len(p) {
			return errors.New("invalid CoC read length")
		}
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
func cocWriteFull(ctx context.Context, stream cocStream, p []byte) error {
	for len(p) > 0 {
		n, err := stream.WriteContext(ctx, p)
		if n < 0 || n > len(p) {
			return errors.New("invalid CoC write length")
		}
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
func newCoCSession(ctx context.Context, stream cocStream, key []byte, generation uint32) (*cocSession, error) {
	hello := make([]byte, 82)
	if err := cocReadFull(ctx, stream, hello[:2]); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint16(hello) != 80 {
		return nil, ErrAuthentication
	}
	if err := cocReadFull(ctx, stream, hello[2:]); err != nil {
		return nil, err
	}
	if string(hello[2:6]) != "PPC1" || binary.LittleEndian.Uint32(hello[6:]) != generation ||
		binary.LittleEndian.Uint32(hello[10:]) == 0 || !hmac.Equal(tag(key, "pp-coc-v1/hello", hello[2:50]), hello[50:]) {
		return nil, ErrAuthentication
	}
	sendMax, receiveMax := int(binary.LittleEndian.Uint16(hello[14:])), int(binary.LittleEndian.Uint16(hello[16:]))
	if sendMax < 54 || sendMax > 4058 || receiveMax < 64 || receiveMax > 4058 {
		return nil, ErrAuthentication
	}
	return &cocSession{stream: stream, sendKey: tag(key, "pp-coc-v1/c2s", hello[18:50]),
		receiveKey: tag(key, "pp-coc-v1/s2c", hello[18:50]), sendMax: sendMax, receiveMax: receiveMax}, nil
}
func (s *cocSession) write(ctx context.Context, p []byte) error {
	if len(p) == 0 || len(p) > s.sendMax || s.sendSeq == math.MaxUint32 {
		return errors.New("CoC record exceeds session bounds")
	}
	s.sendSeq++
	wire := make([]byte, 6, 38+len(p))
	binary.LittleEndian.PutUint16(wire, uint16(36+len(p)))
	binary.LittleEndian.PutUint32(wire[2:], s.sendSeq)
	wire = append(wire, p...)
	wire = append(wire, tag(s.sendKey, "", wire[2:])...)
	return cocWriteFull(ctx, s.stream, wire)
}
func (s *cocSession) read(ctx context.Context) ([]byte, error) {
	prefix := make([]byte, 2)
	if err := cocReadFull(ctx, s.stream, prefix); err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint16(prefix))
	if n < 37 || n > s.receiveMax+36 {
		return nil, ErrAuthentication
	}
	wire := make([]byte, n)
	if err := cocReadFull(ctx, s.stream, wire); err != nil {
		return nil, err
	}
	if s.receiveSeq == math.MaxUint32 || binary.LittleEndian.Uint32(wire) != s.receiveSeq+1 ||
		!hmac.Equal(tag(s.receiveKey, "", wire[:n-32]), wire[n-32:]) {
		return nil, ErrAuthentication
	}
	s.receiveSeq++
	return wire[4 : n-32], nil
}
func (s *cocSession) receipt(ctx context.Context, op byte, id [16]byte) (appImageReceipt, error) {
	p, err := s.read(ctx)
	if err != nil {
		return appImageReceipt{}, err
	}
	r, err := parseAppImageReceipt(p)
	if err == nil && (r.op != op || r.id != id) {
		return r, imageFailure(errors.New("CoC application receipt does not match command"))
	}
	return r, err
}
func (s *AppImageSender) sendCoC(ctx context.Context, c *Client, target []byte, tx *appImageTransaction, data []byte, progress func(int, int)) error {
	opener := c.link.(cocOpener)
	stream, err := opener.openCoC(ctx, c.cocPSM)
	if err != nil {
		return err
	}
	defer stream.Close()
	handshake, cancel := context.WithTimeout(ctx, 15*time.Second)
	channel, err := newCoCSession(handshake, stream, c.key, binary.LittleEndian.Uint32(target))
	cancel()
	if err != nil {
		return err
	}
	s.mu.Lock()
	committed := tx.committed
	s.mu.Unlock()
	if committed {
		if err = channel.write(ctx, appImageWire(appImageQuery, tx.id, nil)); err != nil {
			return err
		}
		r, err := channel.receipt(ctx, appImageQuery, tx.id)
		if err != nil {
			return err
		}
		return s.receipt(tx, r)
	}
	s.reportTransport("l2cap-coc")
	begin := make([]byte, 36)
	binary.LittleEndian.PutUint32(begin, tx.size)
	copy(begin[4:], tx.digest[:])
	if err = channel.write(ctx, appImageWire(appImageBegin, tx.id, begin)); err != nil {
		return err
	}
	r, err := channel.receipt(ctx, appImageBegin, tx.id)
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
		return imageFailure(errors.New("invalid CoC image begin receipt"))
	}
	for offset := int(r.received); offset < len(data); {
		end := min(offset+channel.sendMax-22, len(data))
		tail := make([]byte, 4, 4+end-offset)
		binary.LittleEndian.PutUint32(tail, uint32(offset))
		tail = append(tail, data[offset:end]...)
		if err = channel.write(ctx, appImageWire(appImageChunk, tx.id, tail)); err != nil {
			return err
		}
		offset = end
		if progress != nil {
			progress(offset, len(data))
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	tx.committed = true
	tx.target = append([]byte(nil), target...)
	s.uncertain[tx.digest] = tx
	s.mu.Unlock()
	if err = channel.write(ctx, appImageWire(appImageCommit, tx.id, nil)); err != nil {
		return err
	}
	r, err = channel.receipt(ctx, appImageCommit, tx.id)
	if err != nil {
		return err
	}
	return s.receipt(tx, r)
}
