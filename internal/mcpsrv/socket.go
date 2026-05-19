package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skitzo2000/ai-hardware-buddy/internal/protocol"
	"github.com/skitzo2000/ai-hardware-buddy/internal/transcript"
)

// SocketRequest is the wire shape sent over the Unix socket from a CLI
// invocation (hwbuddy approve, hwbuddy event, …) to the running MCP server.
// One JSON object per line; reply is one JSON object per line.
type SocketRequest struct {
	Op   string         `json:"op"`
	Data map[string]any `json:"data,omitempty"`
}

// SocketReply mirrors the MCP-tool result shape so both transports look the
// same to clients.
type SocketReply struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data,omitempty"`
	Error string         `json:"error,omitempty"`
}

// SocketPath returns the path where the MCP server's Unix socket lives.
// Honours $XDG_RUNTIME_DIR; falls back to ~/.cache.
func SocketPath() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "hwbuddy.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "hwbuddy.sock")
}

// AnotherInstanceRunning reports whether a live hwbuddy MCP server is
// already bound to the socket. Used by `serve` startup to refuse to run
// a duplicate (the common Claude Code /reload-plugins race that leaves
// orphan daemons fighting over the BLE adapter). If the socket file
// exists but no process answers, returns false — caller can safely
// unlink and bind fresh.
func AnotherInstanceRunning() bool {
	path := SocketPath()
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 750*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(750 * time.Millisecond))
	if _, err := conn.Write([]byte(`{"op":"ping"}` + "\n")); err != nil {
		return false
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	// Any well-formed reply means there's a live server.
	return true
}

// serveSocket runs an accept loop on the Unix socket. Each connection is a
// short-lived request/reply line. Returns when ctx is canceled.
func (s *Server) serveSocket(ctx context.Context) {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("socket dir: %v", err)
		return
	}
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("socket listen: %v", err)
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		log.Printf("socket chmod: %v", err)
	}
	log.Printf("listening on %s", path)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(path)
	}()

	var connWG sync.WaitGroup
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("socket accept: %v", err)
			}
			break
		}
		connWG.Add(1)
		go func(c net.Conn) {
			defer connWG.Done()
			defer c.Close()
			s.handleSocketConn(ctx, c)
		}(c)
	}
	connWG.Wait()
}

func (s *Server) handleSocketConn(ctx context.Context, c net.Conn) {
	r := bufio.NewReader(c)
	w := json.NewEncoder(c)

	// One request per connection — keeps it stateless and easy for the CLI.
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req SocketRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = w.Encode(SocketReply{OK: false, Error: "invalid json"})
		return
	}

	reply := s.dispatchSocket(ctx, req)
	_ = w.Encode(reply)
}

// dispatchSocket runs the same backend the MCP tools call. Each op maps to
// the matching tool handler's logic; sharing the implementations keeps both
// transports in lock-step.
func (s *Server) dispatchSocket(ctx context.Context, req SocketRequest) SocketReply {
	switch req.Op {
	case "ping":
		return SocketReply{OK: true, Data: map[string]any{
			"version":   Version,
			"connected": s.ble.IsConnected(),
			"address":   s.ble.Address(),
		}}

	case "connect":
		addr, _ := req.Data["address"].(string)
		if err := s.ble.Connect(ctx, addr); err != nil {
			return SocketReply{OK: false, Error: err.Error()}
		}
		s.setDesiredAddress(s.ble.Address())
		s.postConnectAnnounce(ctx)
		return SocketReply{OK: true, Data: map[string]any{"address": s.ble.Address()}}

	case "disconnect":
		if err := s.ble.Disconnect(); err != nil {
			return SocketReply{OK: false, Error: err.Error()}
		}
		return SocketReply{OK: true}

	case "status":
		st := statusFromMap(req.Data)
		if err := s.ble.Send(st); err != nil {
			return SocketReply{OK: false, Error: err.Error()}
		}
		return SocketReply{OK: true}

	case "test":
		id := fmt.Sprintf("test-%d", time.Now().UnixNano())
		push := protocol.PromptPush{
			Prompt:  &protocol.Prompt{ID: id, Tool: "Bash", Hint: "rm -rf /tmp/example"},
			Waiting: 1,
		}
		if err := s.ble.Send(push); err != nil {
			return SocketReply{OK: false, Error: err.Error()}
		}
		return SocketReply{OK: true, Data: map[string]any{"id": id}}

	case "approve":
		return s.approveSocket(ctx, req.Data)

	case "event":
		return s.eventSocket(req.Data)

	case "tokens":
		return s.tokensSocket(req.Data)

	default:
		return SocketReply{OK: false, Error: "unknown op: " + req.Op}
	}
}

func (s *Server) approveSocket(ctx context.Context, data map[string]any) SocketReply {
	id, _ := data["id"].(string)
	if id == "" {
		return SocketReply{OK: false, Error: "id required"}
	}
	tool, _ := data["tool"].(string)
	hint, _ := data["hint"].(string)
	if tool == "" {
		tool = "?"
	}
	timeout := 60 * time.Second
	if v, ok := data["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}

	if !s.ble.IsConnected() {
		return SocketReply{OK: true, Data: map[string]any{"decision": "allow", "reason": "device offline"}}
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
		return SocketReply{OK: false, Error: err.Error()}
	}
	select {
	case dec := <-ch:
		return SocketReply{OK: true, Data: map[string]any{"decision": dec}}
	case <-time.After(timeout):
		return SocketReply{OK: true, Data: map[string]any{"decision": "allow", "reason": "timeout"}}
	case <-ctx.Done():
		return SocketReply{OK: true, Data: map[string]any{"decision": "allow", "reason": "cancelled"}}
	}
}

func (s *Server) eventSocket(payload map[string]any) SocketReply {
	if !s.ble.IsConnected() {
		return SocketReply{OK: true, Data: map[string]any{"sent": false, "reason": "device offline"}}
	}
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
		return SocketReply{OK: false, Error: err.Error()}
	}
	return SocketReply{OK: true, Data: map[string]any{"sent": true, "event": event}}
}

func (s *Server) tokensSocket(data map[string]any) SocketReply {
	tpath, _ := data["transcript_path"].(string)
	if tpath == "" {
		home, _ := os.UserHomeDir()
		cwd, _ := os.Getwd()
		tpath = transcript.FindCurrentTranscript(home, cwd)
	}
	if tpath == "" {
		return SocketReply{OK: false, Error: "no transcript path resolved"}
	}
	u, err := transcript.Summarize(tpath, time.Now().UTC())
	if err != nil {
		return SocketReply{OK: false, Error: err.Error()}
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
		return SocketReply{OK: false, Error: err.Error()}
	}
	return SocketReply{OK: true, Data: map[string]any{
		"lifetime":           u.Lifetime,
		"today":              u.Today,
		"window_5h":          u.Window5h,
		"window_resets_in_s": u.WindowResetsInS,
	}}
}

func statusFromMap(m map[string]any) protocol.Status {
	st := protocol.Status{}
	if v, ok := m["msg"].(string); ok {
		st.Msg = v
	}
	if v, ok := m["total"].(float64); ok {
		st.Total = protocol.IntPtr(int(v))
	}
	if v, ok := m["running"].(float64); ok {
		st.Running = protocol.IntPtr(int(v))
	}
	if v, ok := m["waiting"].(float64); ok {
		st.Waiting = protocol.IntPtr(int(v))
	}
	if v, ok := m["completed"].(bool); ok {
		st.Completed = protocol.BoolPtr(v)
	}
	return st
}
