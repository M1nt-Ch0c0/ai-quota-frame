//go:build windows

package ble

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockBondFile(f *os.File) (func(), error) {
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped) }, nil
}

func publishBondFile(source, target string, candidate bool) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if !candidate {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	// Omitting REPLACE_EXISTING gives candidates create-if-absent behavior;
	// WRITE_THROUGH covers publication where directory fsync is unavailable.
	return windows.MoveFileEx(from, to, flags)
}
