package ble

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func shellFake(output []byte, exit byte) (*fakeLink, *int) {
	f := newFake()
	executions := 0
	f.handle = func(op byte, p []byte) ([]byte, DeviceError, bool, error) {
		if op != OpShell {
			return nil, 0, false, nil
		}
		offset := binary.LittleEndian.Uint32(p)
		if offset == 0 {
			executions++
		} else if len(p) != 4 {
			return nil, 4, true, nil
		}
		end := min(int(offset)+400, len(output))
		data := binary.LittleEndian.AppendUint32(nil, uint32(len(output)))
		data = binary.LittleEndian.AppendUint32(data, offset)
		data = append(data, exit, 0)
		return append(data, output[offset:end]...), 0, true, nil
	}
	return f, &executions
}

func TestShellReadsAllPagesFromOneExecution(t *testing.T) {
	output := []byte(strings.Repeat("0123456789", 101))
	f, executions := shellFake(output, 0)
	result, err := connectFake(t, f).Shell(context.Background(), "apps")
	if err != nil || !bytes.Equal(result.Output, output) || result.Exit != 0 || *executions != 1 {
		t.Fatalf("result=%q err=%v executions=%d", result.Output, err, *executions)
	}
	if got := f.requests[1][5 : len(f.requests[1])-32]; !bytes.Equal(got[4:], []byte("apps")) {
		t.Fatalf("first page request = %q", got)
	}
	if last := f.requests[len(f.requests)-1]; len(last) != 5+4+32 {
		t.Fatalf("continuation carried a command: %d bytes", len(last))
	}
}

func TestShellEmptyOutputAndExitCode(t *testing.T) {
	f, _ := shellFake(nil, 127)
	result, err := connectFake(t, f).Shell(context.Background(), "nope")
	if err != nil || len(result.Output) != 0 || result.Exit != 127 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestShellUnsupportedFirmwareAndValidation(t *testing.T) {
	f := newFake()
	f.handle = func(op byte, _ []byte) ([]byte, DeviceError, bool, error) {
		return nil, 4, op == OpShell, nil
	}
	c := connectFake(t, f)
	if _, err := c.Shell(context.Background(), "uname"); !errors.Is(err, ErrShellUnsupported) {
		t.Fatalf("err = %v", err)
	}
	for _, line := range []string{"", "a\nb", strings.Repeat("x", MaxShellCommand+1), "\xff"} {
		if ValidateShellCommand(line) == nil {
			t.Fatalf("accepted %q", line)
		}
	}
	f = newFake()
	f.handle = func(op byte, p []byte) ([]byte, DeviceError, bool, error) {
		if op != OpInfo || len(p) != 0 {
			return nil, 4, true, nil
		}
		return bytes.Clone(f.info), 0, true, nil
	}
	if err := connectFake(t, f).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
