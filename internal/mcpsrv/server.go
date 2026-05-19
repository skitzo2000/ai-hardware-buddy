// Package mcpsrv exposes BLE buddy state as MCP tools.
//
// The MCP server runs over stdio. Claude Code launches it from the
// plugin's `.mcp.json` config; one instance per Claude Code session.
// Hooks and slash commands invoke its tools to push status/prompts and
// receive approval decisions.
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/skitzo2000/ai-hardware-buddy/internal/ble"
	"github.com/skitzo2000/ai-hardware-buddy/internal/protocol"
	"github.com/skitzo2000/ai-hardware-buddy/internal/transcript"
)

// Version is wired in by main via -ldflags or simple var override.
var Version = "0.2.0-dev"

// keepaliveInterval is shorter than the firmware's 30s connected-state
// timeout so the device's dataConnected() stays true while we're idle.
const keepaliveInterval = 12 * time.Second

// reconnectInterval is the upper bound between reconnect attempts when a
// desired address is set and the BLE link is down. Buddy.OnDisconnect
// nudges us awake earlier on a clean drop.
const reconnectInterval = 8 * time.Second

// Server wires the BLE client into MCP tools.
type Server struct {
	ble *ble.Buddy

	mu              sync.Mutex
	pending         map[string]chan string // approval future, keyed by prompt id
	desiredAddress  string
	reconnectSignal chan struct{}
}

// New builds a configured Server. Pass an enabled-and-ready BLE client.
func New(buddy *ble.Buddy) *Server {
	s := &Server{
		ble:             buddy,
		pending:         make(map[string]chan string),
		reconnectSignal: make(chan struct{}, 1),
	}
	buddy.OnMessage(s.onDeviceMessage)
	buddy.OnDisconnect(s.onBLEDisconnect)
	return s
}

func (s *Server) onBLEDisconnect() {
	log.Printf("ble link dropped; waking reconnect loop")
	select {
	case s.reconnectSignal <- struct{}{}:
	default:
	}
}

func (s *Server) setDesiredAddress(addr string) {
	s.mu.Lock()
	s.desiredAddress = addr
	s.mu.Unlock()
}

func (s *Server) getDesiredAddress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desiredAddress
}

// Serve registers tools and runs the MCP server over stdio. Blocks until
// stdin EOFs or ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	srv := server.NewMCPServer("hardware-buddy", Version,
		server.WithToolCapabilities(false),
	)

	srv.AddTool(mcp.NewTool("connect",
		mcp.WithDescription("Attach the buddy to a paired Claude-XXXX device."),
		mcp.WithString("address",
			mcp.Description("BLE MAC (e.g. EC:E3:34:66:B5:22). Omit to scan."),
		),
	), s.handleConnect)

	srv.AddTool(mcp.NewTool("disconnect",
		mcp.WithDescription("Release the BLE link."),
	), s.handleDisconnect)

	srv.AddTool(mcp.NewTool("status",
		mcp.WithDescription("Push session counters + an optional msg to the device HUD."),
		mcp.WithNumber("total"),
		mcp.WithNumber("running"),
		mcp.WithNumber("waiting"),
		mcp.WithBoolean("completed"),
		mcp.WithString("msg",
			mcp.Description("Short status line, ≤23 chars (truncated for the firmware buffer)."),
		),
	), s.handleStatus)

	srv.AddTool(mcp.NewTool("approve",
		mcp.WithDescription("Show an approval prompt on the device and block until the user taps. Returns decision=allow|deny."),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("Unique id for the prompt; the firmware ack echoes it."),
		),
		mcp.WithString("tool", mcp.Required(),
			mcp.Description("Tool name to display (size 3, ≤13 chars before falling to size 2)."),
		),
		mcp.WithString("hint",
			mcp.Description("One-line hint (≤80 chars)."),
		),
		mcp.WithNumber("timeout_seconds",
			mcp.Description("Max seconds to wait for a tap. Defaults to 60. Times out → allow (fail open)."),
		),
	), s.handleApprove)

	srv.AddTool(mcp.NewTool("event",
		mcp.WithDescription("Forward a Claude Code hook event payload to the device. Pass the raw hook JSON."),
		mcp.WithObject("payload", mcp.Required()),
	), s.handleEvent)

	srv.AddTool(mcp.NewTool("tokens",
		mcp.WithDescription("Parse a Claude Code transcript JSONL and push token usage to the device."),
		mcp.WithString("transcript_path",
			mcp.Description("Absolute path to a Claude Code session JSONL. Defaults to most recent in ~/.claude/projects/<cwd-slug>/."),
		),
	), s.handleTokens)

	srv.AddTool(mcp.NewTool("test",
		mcp.WithDescription("Show a synthetic approval prompt — confirms BLE round trip + touch zones."),
	), s.handleTest)

	srv.AddTool(mcp.NewTool("ping",
		mcp.WithDescription("Report MCP server liveness and BLE link state."),
	), s.handlePing)

	go s.runKeepalive(ctx)
	go s.runReconnect(ctx)
	go s.serveSocket(ctx)

	addr := os.Getenv("HWBUDDY_ADDRESS")
	if addr != "" {
		s.setDesiredAddress(addr)
		go func() {
			if err := s.ble.Connect(ctx, addr); err != nil {
				log.Printf("startup connect failed: %v", err)
			} else {
				s.postConnectAnnounce(ctx)
			}
		}()
	}

	return server.ServeStdio(srv)
}

// ─── tool handlers ───

func (s *Server) handleConnect(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	addr := stringArg(req, "address", "")
	if err := s.ble.Connect(ctx, addr); err != nil {
		return mcpError("connect failed: " + err.Error()), nil
	}
	// Remember the address so reconnectLoop can re-attach silently if the
	// link drops later.
	s.setDesiredAddress(s.ble.Address())
	s.postConnectAnnounce(ctx)
	return mcpOK(map[string]any{"connected": true, "address": s.ble.Address()}), nil
}

func (s *Server) handleDisconnect(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ble.Disconnect(); err != nil {
		return mcpError("disconnect failed: " + err.Error()), nil
	}
	return mcpOK(map[string]any{"connected": false}), nil
}

func (s *Server) handleStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	st := protocol.Status{
		Msg: stringArg(req, "msg", ""),
	}
	if v, ok := numberArg(req, "total"); ok {
		st.Total = protocol.IntPtr(int(v))
	}
	if v, ok := numberArg(req, "running"); ok {
		st.Running = protocol.IntPtr(int(v))
	}
	if v, ok := numberArg(req, "waiting"); ok {
		st.Waiting = protocol.IntPtr(int(v))
	}
	if v, ok := boolArg(req, "completed"); ok {
		st.Completed = protocol.BoolPtr(v)
	}
	if err := s.ble.Send(st); err != nil {
		return mcpError(err.Error()), nil
	}
	return mcpOK(nil), nil
}

func (s *Server) handleApprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := stringArg(req, "id", "")
	if id == "" {
		return mcpError("id required"), nil
	}
	tool := stringArg(req, "tool", "?")
	hint := stringArg(req, "hint", "")

	timeout := 60 * time.Second
	if v, ok := numberArg(req, "timeout_seconds"); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}

	if !s.ble.IsConnected() {
		// Fail-open: BLE offline → allow so Claude Code never blocks on
		// a hardware that isn't reachable.
		return mcpOK(map[string]any{"decision": "allow", "reason": "device offline"}), nil
	}

	ch := make(chan string, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		_ = s.ble.Send(protocol.PromptPush{ClearPrompt: true, Waiting: 0})
	}()

	push := protocol.PromptPush{
		Prompt:  &protocol.Prompt{ID: id, Tool: truncate(tool, 31), Hint: truncate(hint, 79)},
		Waiting: 1,
	}
	if err := s.ble.Send(push); err != nil {
		return mcpError("send prompt: " + err.Error()), nil
	}

	select {
	case dec := <-ch:
		return mcpOK(map[string]any{"decision": dec}), nil
	case <-time.After(timeout):
		return mcpOK(map[string]any{"decision": "allow", "reason": "timeout"}), nil
	case <-ctx.Done():
		return mcpOK(map[string]any{"decision": "allow", "reason": "cancelled"}), nil
	}
}

func (s *Server) handleEvent(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.ble.IsConnected() {
		return mcpOK(map[string]any{"sent": false, "reason": "device offline"}), nil
	}

	payload := objectArg(req, "payload")
	event, _ := payload["hook_event_name"].(string)

	st := s.eventStatus(event, payload)
	if tpath, _ := payload["transcript_path"].(string); tpath != "" {
		if u, err := transcript.Summarize(tpath, time.Now().UTC()); err == nil {
			st.Tokens = protocol.U32Ptr(u.Lifetime)
			st.TokensToday = protocol.U32Ptr(u.Today)
			if u.WindowResetsInS > 0 {
				st.TokensWindow5h = protocol.U32Ptr(u.Window5h)
				st.WindowResetS = protocol.IntPtr(u.WindowResetsInS)
			}
		}
	}
	if err := s.ble.Send(st); err != nil {
		return mcpError(err.Error()), nil
	}
	return mcpOK(map[string]any{"sent": true, "event": event}), nil
}

func (s *Server) handleTokens(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tpath := stringArg(req, "transcript_path", "")
	if tpath == "" {
		home, _ := os.UserHomeDir()
		cwd, _ := os.Getwd()
		tpath = transcript.FindCurrentTranscript(home, cwd)
	}
	if tpath == "" {
		return mcpError("no transcript path resolved; pass transcript_path explicitly"), nil
	}
	u, err := transcript.Summarize(tpath, time.Now().UTC())
	if err != nil {
		return mcpError("parse transcript: " + err.Error()), nil
	}

	st := protocol.Status{
		Msg:         "tokens sync",
		Tokens:      protocol.U32Ptr(u.Lifetime),
		TokensToday: protocol.U32Ptr(u.Today),
	}
	if u.WindowResetsInS > 0 {
		st.TokensWindow5h = protocol.U32Ptr(u.Window5h)
		st.WindowResetS = protocol.IntPtr(u.WindowResetsInS)
	}
	if err := s.ble.Send(st); err != nil {
		return mcpError(err.Error()), nil
	}
	return mcpOK(map[string]any{
		"transcript":          tpath,
		"lifetime":            u.Lifetime,
		"today":               u.Today,
		"window_5h":           u.Window5h,
		"window_resets_in_s":  u.WindowResetsInS,
	}), nil
}

func (s *Server) handleTest(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	push := protocol.PromptPush{
		Prompt:  &protocol.Prompt{ID: id, Tool: "Bash", Hint: "rm -rf /tmp/example"},
		Waiting: 1,
	}
	if err := s.ble.Send(push); err != nil {
		return mcpError(err.Error()), nil
	}
	return mcpOK(map[string]any{"id": id, "note": "approval bar should appear on the device for ~5s"}), nil
}

func (s *Server) handlePing(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpOK(map[string]any{
		"version":   Version,
		"connected": s.ble.IsConnected(),
		"address":   s.ble.Address(),
	}), nil
}

// ─── event → status mapping ───

func (s *Server) eventStatus(event string, payload map[string]any) protocol.Status {
	switch event {
	case "SessionStart":
		return protocol.Status{Running: protocol.IntPtr(1), Msg: "session started"}
	case "UserPromptSubmit":
		prompt, _ := payload["prompt"].(string)
		st := protocol.Status{Running: protocol.IntPtr(1), Msg: "thinking…"}
		if prompt != "" {
			st.Entries = []string{truncate(prompt, 90)}
		}
		return st
	case "PostToolUse":
		tool, _ := payload["tool_name"].(string)
		return protocol.Status{
			Running:   protocol.IntPtr(1),
			Msg:       fmt.Sprintf("%s done", truncate(tool, 14)),
			Completed: protocol.BoolPtr(true),
		}
	case "Notification":
		text, _ := payload["message"].(string)
		return protocol.Status{Msg: truncate(text, 23)}
	case "Stop", "SubagentStop":
		return protocol.Status{
			Running: protocol.IntPtr(0),
			Waiting: protocol.IntPtr(0),
			Msg:     "idle",
		}
	case "PreCompact":
		return protocol.Status{Running: protocol.IntPtr(1), Msg: "compacting…"}
	default:
		return protocol.Status{Msg: truncate(strings.TrimSpace(event), 23)}
	}
}

// ─── device callback ───

func (s *Server) onDeviceMessage(obj map[string]any) {
	cmd, _ := obj["cmd"].(string)
	if cmd != "permission" {
		return
	}
	id, _ := obj["id"].(string)
	raw, _ := obj["decision"].(string)
	var decision string
	switch raw {
	case "once", "always":
		decision = "allow"
	case "deny":
		decision = "deny"
	default:
		return
	}

	s.mu.Lock()
	ch, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- decision:
	default:
	}
}

// ─── background tasks ───

// runReconnect retries Buddy.Connect whenever a desired address is set and
// the link is down. Woken on Buddy.OnDisconnect for fast recovery, plus a
// periodic 8-second tick as a fallback for drops we somehow miss.
func (s *Server) runReconnect(ctx context.Context) {
	t := time.NewTicker(reconnectInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.reconnectSignal:
		}

		addr := s.getDesiredAddress()
		if addr == "" || s.ble.IsConnected() {
			continue
		}

		log.Printf("ble: reconnect attempt to %s", addr)
		if err := s.ble.Connect(ctx, addr); err != nil {
			log.Printf("ble: reconnect failed: %v", err)
			continue
		}
		s.postConnectAnnounce(ctx)
	}
}

func (s *Server) runKeepalive(ctx context.Context) {
	t := time.NewTicker(keepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.ble.IsConnected() {
				continue
			}
			if time.Since(s.ble.LastSend()) < keepaliveInterval-time.Second {
				continue
			}
			_ = s.ble.Send(map[string]any{"keepalive": time.Now().UnixMilli()})
		}
	}
}

func (s *Server) postConnectAnnounce(_ context.Context) {
	// Clear the firmware's stale "No Claude connected" tama.msg by pushing
	// a real status with an explicit msg field.
	_ = s.ble.Send(protocol.Status{Msg: "Claude Code online"})
}

// ─── arg helpers ───

func stringArg(req mcp.CallToolRequest, name, def string) string {
	if v, ok := req.GetArguments()[name].(string); ok && v != "" {
		return v
	}
	return def
}

func numberArg(req mcp.CallToolRequest, name string) (float64, bool) {
	switch v := req.GetArguments()[name].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

func boolArg(req mcp.CallToolRequest, name string) (bool, bool) {
	v, ok := req.GetArguments()[name].(bool)
	return v, ok
}

func objectArg(req mcp.CallToolRequest, name string) map[string]any {
	v, _ := req.GetArguments()[name].(map[string]any)
	if v == nil {
		v = map[string]any{}
	}
	return v
}

// ─── result helpers ───

func mcpOK(data map[string]any) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]any{"ok": true, "data": data})
	return mcp.NewToolResultText(string(body))
}

func mcpError(msg string) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return mcp.NewToolResultText(string(body))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Suppress unused import warnings when developing without all helpers used.
var _ = errors.New
var _ = filepath.Join
