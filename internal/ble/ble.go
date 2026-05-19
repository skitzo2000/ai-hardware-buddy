// Package ble holds one BLE connection to a Claude-XXXX device for the
// lifetime of the MCP server process. Uses tinygo-org/bluetooth which wraps
// BlueZ (Linux), CoreBluetooth (macOS) and WinRT (Windows) under a single
// adapter/device/characteristic API.
//
// LE Secure Connections bonding must already be in place at the OS level
// (one-time `bluetoothctl pair` flow on Linux; System Settings on macOS).
// This package does not run a pairing agent.
package ble

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"github.com/skitzo2000/ai-hardware-buddy/internal/protocol"
)

// Buddy is the single-connection BLE client.
type Buddy struct {
	adapter *bluetooth.Adapter

	mu        sync.Mutex
	device    *bluetooth.Device
	rxChar    *bluetooth.DeviceCharacteristic
	txChar    *bluetooth.DeviceCharacteristic
	address   string

	rxBuf        []byte
	onMessage    func(map[string]any)
	onDisconnect func()
	lastSendAt   time.Time
}

// New creates a Buddy ready to connect. Call Enable() before Connect().
func New() *Buddy {
	return &Buddy{
		adapter: bluetooth.DefaultAdapter,
	}
}

// Enable powers up the adapter. Idempotent.
func (b *Buddy) Enable() error {
	return b.adapter.Enable()
}

// IsConnected reports the last known link state.
func (b *Buddy) IsConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.device != nil
}

// Address returns the MAC the client is (or was last) attached to.
func (b *Buddy) Address() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.address
}

// LastSend returns when the last GATT write succeeded — used by the
// keepalive task to suppress unnecessary heartbeats.
func (b *Buddy) LastSend() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSendAt
}

// OnMessage registers a callback invoked for every JSON object the device
// notifies on the TX characteristic.
func (b *Buddy) OnMessage(fn func(map[string]any)) {
	b.onMessage = fn
}

// OnDisconnect registers a callback fired whenever Buddy notices the BLE
// link has died (via write failure or explicit Disconnect). The reconnect
// loop in the daemon hooks this to retry immediately rather than waiting
// for its next periodic tick.
func (b *Buddy) OnDisconnect(fn func()) {
	b.onDisconnect = fn
}

// Discover scans for `timeout` and returns the first Claude-* device found.
func (b *Buddy) Discover(ctx context.Context, timeout time.Duration) (string, error) {
	resultCh := make(chan bluetooth.ScanResult, 1)
	done := make(chan struct{})

	go func() {
		select {
		case <-time.After(timeout):
		case <-ctx.Done():
		case <-done:
		}
		_ = b.adapter.StopScan()
	}()

	err := b.adapter.Scan(func(_ *bluetooth.Adapter, sr bluetooth.ScanResult) {
		name := sr.LocalName()
		if strings.HasPrefix(name, protocol.DeviceNamePrefix) {
			select {
			case resultCh <- sr:
				close(done)
			default:
			}
		}
	})
	// adapter.Scan blocks until StopScan; the goroutine above triggers it.
	if err != nil {
		return "", err
	}

	select {
	case sr := <-resultCh:
		return sr.Address.String(), nil
	default:
		return "", fmt.Errorf("no Claude-* device in range after %s", timeout)
	}
}

// Connect attaches to the device at `address`. Discovers if `address` is
// empty. The companion adapter must be enabled. If the first attempt fails
// with a "device not found" error and `address` is known (the most common
// Linux failure mode — BlueZ is holding a connection to a trusted device
// so it's not advertising and tinygo-bluetooth can't see it), we shell out
// to ``bluetoothctl disconnect <MAC>`` and retry once.
func (b *Buddy) Connect(ctx context.Context, address string) error {
	b.mu.Lock()
	if b.device != nil {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()

	if address == "" {
		discovered, err := b.Discover(ctx, 8*time.Second)
		if err != nil {
			return fmt.Errorf("discover: %w", err)
		}
		address = discovered
	}

	err := b.attemptConnect(ctx, address)
	if err != nil && isDeviceNotFound(err) {
		log.Printf("ble: first connect failed (%v); releasing BlueZ hold and retrying", err)
		b.bluezDisconnect(ctx, address)
		// brief settle so BlueZ refreshes its object map
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
		err = b.attemptConnect(ctx, address)
	}
	return err
}

func (b *Buddy) attemptConnect(_ context.Context, address string) error {
	addr, err := parseAddress(address)
	if err != nil {
		return err
	}

	dev, err := b.adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("ble connect: %w", err)
	}

	srvUUID, _ := bluetooth.ParseUUID(protocol.NUSService)
	srvs, err := dev.DiscoverServices([]bluetooth.UUID{srvUUID})
	if err != nil || len(srvs) == 0 {
		_ = dev.Disconnect()
		return fmt.Errorf("NUS service not found: %w", err)
	}

	rxUUID, _ := bluetooth.ParseUUID(protocol.NUSRX)
	txUUID, _ := bluetooth.ParseUUID(protocol.NUSTX)
	chars, err := srvs[0].DiscoverCharacteristics([]bluetooth.UUID{rxUUID, txUUID})
	if err != nil || len(chars) < 2 {
		_ = dev.Disconnect()
		return fmt.Errorf("NUS chars not found: %w", err)
	}
	var rx, tx bluetooth.DeviceCharacteristic
	for _, c := range chars {
		switch c.UUID().String() {
		case protocol.NUSRX:
			rx = c
		case protocol.NUSTX:
			tx = c
		}
	}

	if err := tx.EnableNotifications(b.handleNotify); err != nil {
		_ = dev.Disconnect()
		return fmt.Errorf("enable notify: %w", err)
	}

	b.mu.Lock()
	b.device = &dev
	b.rxChar = &rx
	b.txChar = &tx
	b.address = address
	b.mu.Unlock()

	log.Printf("ble: connected to %s", address)
	return nil
}

// bluezDisconnect shells out to `bluetoothctl disconnect MAC` to release
// BlueZ's auto-held connection to a trusted device. The binary handles
// hciN discovery for us; it's already a dependency anywhere tinygo's
// Linux backend runs.
func (b *Buddy) bluezDisconnect(ctx context.Context, address string) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "bluetoothctl", "disconnect", address)
	_ = cmd.Run()
}

func isDeviceNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") ||
		strings.Contains(s, "no such device") ||
		strings.Contains(s, "le-connection-abort-by-local")
}

// Disconnect tears down the link. Safe to call when already disconnected.
func (b *Buddy) Disconnect() error {
	b.mu.Lock()
	dev := b.device
	b.device = nil
	b.rxChar = nil
	b.txChar = nil
	b.mu.Unlock()

	if dev == nil {
		return nil
	}
	return dev.Disconnect()
}

// Send writes a newline-delimited JSON object to the RX characteristic.
func (b *Buddy) Send(obj any) error {
	b.mu.Lock()
	rx := b.rxChar
	b.mu.Unlock()
	if rx == nil {
		return fmt.Errorf("not connected")
	}

	payload, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := rx.WriteWithoutResponse(payload); err != nil {
		// On write failure, assume the link is dead so callers re-attempt.
		b.mu.Lock()
		b.device = nil
		b.rxChar = nil
		b.txChar = nil
		cb := b.onDisconnect
		b.mu.Unlock()
		if cb != nil {
			go cb()
		}
		return fmt.Errorf("write: %w", err)
	}
	b.mu.Lock()
	b.lastSendAt = time.Now()
	b.mu.Unlock()
	return nil
}

func (b *Buddy) handleNotify(data []byte) {
	b.mu.Lock()
	b.rxBuf = append(b.rxBuf, data...)
	for {
		idx := indexByte(b.rxBuf, '\n')
		if idx < 0 {
			break
		}
		line := b.rxBuf[:idx]
		b.rxBuf = b.rxBuf[idx+1:]

		if len(line) == 0 || line[0] != '{' {
			continue
		}
		// Copy line out from under the lock before parsing.
		buf := make([]byte, len(line))
		copy(buf, line)
		cb := b.onMessage
		b.mu.Unlock()

		var obj map[string]any
		if err := json.Unmarshal(buf, &obj); err == nil && cb != nil {
			cb(obj)
		}
		b.mu.Lock()
	}
	b.mu.Unlock()
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
