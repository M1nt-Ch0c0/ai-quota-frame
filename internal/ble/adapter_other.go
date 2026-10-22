//go:build linux || (darwin && cgo)

package ble

import "tinygo.org/x/bluetooth"

func enableAdapter(adapter *bluetooth.Adapter) (func(), error) {
	return func() {}, adapter.Enable()
}
