//go:build linux || windows

package ble

import (
	"fmt"

	"tinygo.org/x/bluetooth"
)

// parseAddress turns a user-supplied identifier into a tinygo bluetooth.Address.
// On linux and windows the identifier is a colon-separated MAC.
func parseAddress(s string) (bluetooth.Address, error) {
	mac, err := bluetooth.ParseMAC(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("parse mac %q: %w", s, err)
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}, nil
}
