package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

// OpShell runs one read-only host shell command. The host captures the output
// once per command; later pages only read that capture and never re-execute.
const OpShell byte = 28

const (
	MaxShellCommand = 256
	maxShellOutput  = 4095
)

var ErrShellUnsupported = errors.New("device firmware has no BLE shell; update the PhotoPainter host")

type ShellResult struct {
	Exit      byte   `json:"exit"`
	Truncated bool   `json:"truncated,omitempty"`
	Output    []byte `json:"output"`
}

// Ping keeps an authenticated session inside the host idle window.
func (c *Client) Ping(ctx context.Context) error {
	data, err := c.command(ctx, OpInfo, nil)
	if err != nil {
		return err
	}
	if _, err = ParseInfo(data); err != nil {
		return errors.New("invalid BLE keepalive response")
	}
	return nil
}

func ValidateShellCommand(line string) error {
	if len(line) == 0 || len(line) > MaxShellCommand || !utf8.ValidString(line) {
		return errors.New("shell command must be 1..256 bytes of UTF-8")
	}
	for _, r := range line {
		// C1 controls are rejected by the host as invalid requests too.
		if r != '\t' && (r < 0x20 || r >= 0x7f && r <= 0x9f) {
			return errors.New("shell command contains control characters")
		}
	}
	return nil
}

func (c *Client) Shell(ctx context.Context, line string) (ShellResult, error) {
	var result ShellResult
	if err := ValidateShellCommand(line); err != nil {
		return result, err
	}
	var total uint32
	for {
		p := make([]byte, 4, 4+len(line))
		binary.LittleEndian.PutUint32(p, uint32(len(result.Output)))
		if len(result.Output) == 0 {
			p = append(p, line...)
		}
		data, err := c.command(ctx, OpShell, p)
		if errors.Is(err, DeviceError(4)) && len(result.Output) == 0 {
			// Hosts without the shell reject unknown ops as invalid requests.
			return result, ErrShellUnsupported
		}
		if err != nil {
			return result, err
		}
		if len(data) < 10 || binary.LittleEndian.Uint32(data[4:8]) != uint32(len(result.Output)) {
			return result, errors.New("inconsistent BLE shell pages")
		}
		n := binary.LittleEndian.Uint32(data[:4])
		if n > maxShellOutput || len(result.Output) > 0 && (n != total || data[8] != result.Exit) {
			return result, errors.New("inconsistent BLE shell pages")
		}
		total, result.Exit, result.Truncated = n, data[8], data[9]&1 != 0
		if len(data) == 10 && total != 0 {
			return result, errors.New("empty BLE shell page")
		}
		result.Output = append(result.Output, data[10:]...)
		if len(result.Output) > int(total) {
			return result, errors.New("oversize BLE shell output")
		}
		if len(result.Output) == int(total) {
			return result, nil
		}
	}
}
