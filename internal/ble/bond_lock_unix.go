//go:build darwin || linux

package ble

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockBondFile(f *os.File) (func(), error) {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }, nil
}

func publishBondFile(source, target string, candidate bool) error {
	if candidate {
		// Publish a synced candidate without replacing another relationship.
		return os.Link(source, target)
	}
	return os.Rename(source, target)
}
