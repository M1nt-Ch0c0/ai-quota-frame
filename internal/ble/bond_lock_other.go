//go:build !darwin && !linux && !windows

package ble

import (
	"errors"
	"os"
)

func lockBondFile(*os.File) (func(), error) {
	return nil, errors.New("native pairing storage is unavailable on this platform")
}

func publishBondFile(string, string, bool) error {
	return errors.New("native pairing storage is unavailable on this platform")
}
