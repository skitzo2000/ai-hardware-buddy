// Package protocol defines the firmware-side BLE wire shapes.
//
// Both directions are newline-delimited JSON over the Nordic UART Service.
// Reference: firmware/src/data.h `_applyJson` and firmware/src/xfer.h.
package protocol

// Nordic UART Service UUIDs.
const (
	NUSService = "6e400001-b5a3-f393-e0a9-e50e24dcca9e"
	NUSRX      = "6e400002-b5a3-f393-e0a9-e50e24dcca9e" // host writes here
	NUSTX      = "6e400003-b5a3-f393-e0a9-e50e24dcca9e" // device notifies here

	DeviceNamePrefix = "Claude-"
)

// Status is the shape pushed for general session updates. Field names match
// what the firmware expects in _applyJson; empty/zero values are omitted by
// json:",omitempty" so we don't clobber state we don't intend to change.
type Status struct {
	Total     *int     `json:"total,omitempty"`
	Running   *int     `json:"running,omitempty"`
	Waiting   *int     `json:"waiting,omitempty"`
	Completed *bool    `json:"completed,omitempty"`
	Msg       string   `json:"msg,omitempty"`
	Entries   []string `json:"entries,omitempty"`

	Tokens         *uint32 `json:"tokens,omitempty"`
	TokensToday    *uint32 `json:"tokens_today,omitempty"`
	TokensWindow5h *uint32 `json:"tokens_window_5h,omitempty"`
	WindowResetS   *int    `json:"window_reset_s,omitempty"`
}

// Prompt is the approval-request shape pushed to display drawApproval on the
// device.
type Prompt struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Hint string `json:"hint"`
}

// PromptPush wraps a Prompt in the {"prompt": {...}, "waiting": 1} envelope
// the firmware parses. To clear an active prompt without disturbing it via
// the "no prompt field present" path (which the firmware now ignores),
// send PromptPush{ClearPrompt: true, Waiting: 0}.
type PromptPush struct {
	Prompt      *Prompt `json:"prompt,omitempty"`
	ClearPrompt bool    `json:"clear_prompt,omitempty"`
	Waiting     int     `json:"waiting"`
}

// PermissionAck is the shape received from the device when the user taps the
// approval prompt. Firmware sends decision as one of "once"|"always"|"deny";
// callers map once/always -> allow.
type PermissionAck struct {
	Cmd      string `json:"cmd"`
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// IntPtr / BoolPtr / U32Ptr are small helpers for the *omitempty fields above.
func IntPtr(v int) *int          { return &v }
func BoolPtr(v bool) *bool       { return &v }
func U32Ptr(v uint32) *uint32    { return &v }
