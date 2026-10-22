package ble

import (
	"errors"
	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
	"tinygo.org/x/bluetooth"
)

func enableAdapter(adapter *bluetooth.Adapter) (func(), error) {
	err := adapter.Enable()
	var comError *ole.OleError
	// RoInitialize's S_FALSE is a successful additional initialization and
	// must still be balanced with RoUninitialize on this same OS thread.
	if err != nil && !(errors.As(err, &comError) && comError.Code() == 1) {
		return func() {}, err
	}
	return func() { windows.NewLazySystemDLL("combase.dll").NewProc("RoUninitialize").Call() }, nil
}
