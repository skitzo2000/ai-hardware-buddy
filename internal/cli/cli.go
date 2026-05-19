// Package cli is the client-side of hwbuddy.
//
// When invoked with a subcommand (e.g. `hwbuddy approve`), the binary
// connects to the running MCP server's Unix socket and forwards the
// request. Hooks use this entry point so they reach the same in-memory
// state (pending approval futures, BLE connection) the MCP tools see.
//
// Silent passthrough: if the socket isn't available (no MCP server
// running, plugin not loaded, etc.), commands exit 0 with a noop so
// Claude Code is never blocked by missing hardware.
package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/skitzo2000/ai-hardware-buddy/internal/mcpsrv"
)

// Run dispatches the given args (excluding the program name) and returns
// the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		printHelp(os.Stdout)
		return 0
	}
	switch args[0] {
	case "approve":
		return runApprove()
	case "event":
		return runEvent()
	case "tokens":
		return runTokens(args[1:])
	case "status":
		return runStatus(args[1:])
	case "connect":
		return runConnect(args[1:])
	case "disconnect":
		return runOpJSON("disconnect", nil)
	case "ping":
		return runOpJSON("ping", nil)
	case "test":
		return runOpJSON("test", nil)
	case "socket-path":
		fmt.Println(mcpsrv.SocketPath())
		return 0
	case "help", "-h", "--help":
		printHelp(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		printHelp(os.Stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `hwbuddy — Claude Code BLE bridge

Run with no args (or `+"`hwbuddy serve`"+`) to start the MCP server. Otherwise:

  hwbuddy ping
  hwbuddy connect [--address MAC]
  hwbuddy disconnect
  hwbuddy status  [--msg STR --running N --waiting N --total N --completed]
  hwbuddy test
  hwbuddy tokens  [--transcript PATH]
  hwbuddy approve  (reads PreToolUse JSON from stdin)
  hwbuddy event    (reads any hook JSON from stdin)
  hwbuddy socket-path
  hwbuddy version`)
}

// ─── hook entrypoints ───

func runApprove() int {
	payload, err := readStdinJSON()
	if err != nil {
		return 0 // fail open on bad input
	}
	tool, _ := payload["tool_name"].(string)
	if tool == "" {
		tool, _ = payload["tool"].(string)
	}
	if tool == "" {
		tool = "?"
	}
	input, _ := payload["tool_input"].(map[string]any)
	hint := summarizeHint(input)

	id, _ := payload["session_id"].(string)
	if id == "" {
		id = fmt.Sprintf("hook-%d", time.Now().UnixNano())
	}
	if len(id) > 39 {
		id = id[:39]
	}

	reply, ok := callSocket("approve", map[string]any{
		"id":   id,
		"tool": tool,
		"hint": hint,
	}, 130*time.Second)
	if !ok || reply == nil {
		return 0 // silent passthrough
	}
	if dec, _ := reply.Data["decision"].(string); dec == "deny" {
		fmt.Fprintf(os.Stderr, "denied via hardware buddy: %s\n", tool)
		return 2
	}
	return 0
}

func runEvent() int {
	payload, err := readStdinJSON()
	if err != nil {
		return 0
	}
	callSocket("event", payload, 5*time.Second)
	return 0
}

func runTokens(args []string) int {
	fs := flag.NewFlagSet("tokens", flag.ContinueOnError)
	tpath := fs.String("transcript", "", "Claude Code transcript JSONL path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body := map[string]any{}
	if *tpath != "" {
		body["transcript_path"] = *tpath
	}
	return runOpJSON("tokens", body)
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	msg := fs.String("msg", "", "status line on the HUD")
	total := fs.Int("total", -1, "total session count")
	running := fs.Int("running", -1, "running session count")
	waiting := fs.Int("waiting", -1, "waiting session count")
	completed := fs.Bool("completed", false, "set completed flag")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body := map[string]any{}
	if *msg != "" {
		body["msg"] = *msg
	}
	if *total >= 0 {
		body["total"] = *total
	}
	if *running >= 0 {
		body["running"] = *running
	}
	if *waiting >= 0 {
		body["waiting"] = *waiting
	}
	if *completed {
		body["completed"] = true
	}
	return runOpJSON("status", body)
}

func runConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	addr := fs.String("address", "", "BLE MAC")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body := map[string]any{}
	if *addr != "" {
		body["address"] = *addr
	}
	return runOpJSON("connect", body)
}

// ─── socket plumbing ───

func runOpJSON(op string, data map[string]any) int {
	reply, ok := callSocket(op, data, 30*time.Second)
	if !ok {
		fmt.Fprintln(os.Stderr, "hwbuddy MCP server is not running (no socket at "+mcpsrv.SocketPath()+")")
		return 1
	}
	out, _ := json.Marshal(reply)
	fmt.Println(string(out))
	if !reply.OK {
		return 1
	}
	return 0
}

func callSocket(op string, data map[string]any, timeout time.Duration) (*mcpsrv.SocketReply, bool) {
	path := mcpsrv.SocketPath()
	conn, err := net.DialTimeout("unix", path, 1*time.Second)
	if err != nil {
		return nil, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req := mcpsrv.SocketRequest{Op: op, Data: data}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, false
	}
	var reply mcpsrv.SocketReply
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&reply); err != nil {
		return nil, false
	}
	return &reply, true
}

func readStdinJSON() (map[string]any, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// summarizeHint pulls a short one-line hint out of Claude Code's tool_input.
func summarizeHint(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern", "query"} {
		if s, ok := input[k].(string); ok && s != "" {
			return s
		}
	}
	for _, v := range input {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
