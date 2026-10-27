//go:build darwin && cgo

package ble

/*
#cgo LDFLAGS: -framework Foundation -framework CoreBluetooth
#include <stdlib.h>
#include "coc_darwin.h"
*/
import "C"
import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
	"unsafe"
)

type macCoC struct {
	mu     sync.Mutex
	handle unsafe.Pointer
}

func configureCoC(link *nativeLink, address string) {
	link.cocOpen = func(ctx context.Context, psm uint16) (cocStream, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timeout := 20 * time.Second
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
			timeout = time.Until(deadline)
		}
		if timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
		name := C.CString(address)
		defer C.free(unsafe.Pointer(name))
		handle := C.pp_coc_open(name, C.uint16_t(psm), C.uint32_t(max(1, timeout.Milliseconds())))
		if handle == nil {
			return nil, errors.New("CoreBluetooth L2CAP CoC connection failed")
		}
		stream := &macCoC{handle: handle}
		if err := ctx.Err(); err != nil {
			stream.Close()
			return nil, err
		}
		return stream, nil
	}
}
func (s *macCoC) ReadContext(ctx context.Context, p []byte) (int, error) {
	return s.transfer(ctx, p, false)
}
func (s *macCoC) WriteContext(ctx context.Context, p []byte) (int, error) {
	return s.transfer(ctx, p, true)
}
func (s *macCoC) transfer(ctx context.Context, p []byte, write bool) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		s.mu.Lock()
		if s.handle == nil {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		var n C.int
		if write {
			n = C.pp_coc_write(s.handle, unsafe.Pointer(&p[0]), C.int(min(len(p), 4096)))
		} else {
			n = C.pp_coc_read(s.handle, unsafe.Pointer(&p[0]), C.int(min(len(p), 4096)))
		}
		s.mu.Unlock()
		if n < 0 {
			return 0, io.ErrUnexpectedEOF
		}
		if n > 0 {
			return int(n), nil
		}
		if err := wait(ctx, 2*time.Millisecond); err != nil {
			return 0, err
		}
	}
}
func (s *macCoC) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle != nil {
		C.pp_coc_close(s.handle)
		s.handle = nil
	}
	return nil
}
