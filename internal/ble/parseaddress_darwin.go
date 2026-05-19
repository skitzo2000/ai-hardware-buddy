//go:build darwin

package ble

import (
	"fmt"

	"tinygo.org/x/bluetooth"
)

// parseAddress turns a user-supplied identifier into a tinygo bluetooth.Address.
// CoreBluetooth never exposes the BLE MAC; peripherals are identified by a
// system-assigned UUID instead. Discover() returns that UUID via
// ScanResult.Address.String() on macOS, so the auto-discovered reconnect
// flow works without any user input.
func parseAddress(s string) (bluetooth.Address, error) {
	uuid, err := bluetooth.ParseUUID(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("parse peripheral uuid %q: %w", s, err)
	}
	return bluetooth.Address{UUID: uuid}, nil
}
