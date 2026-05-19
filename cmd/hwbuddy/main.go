// Command hwbuddy is the MCP server that bridges Claude Code session events
// to a Claude-XXXX BLE device. Single static binary; ships in the plugin's
// bin/ directory.
//
// Two modes:
//   - no args (or `serve`): MCP server over stdio plus a Unix socket for
//     hook subprocesses. Claude Code launches this from the plugin's
//     .mcp.json config.
//   - subcommand (approve, event, tokens, status, connect, …): CLI client.
//     Talks to the running MCP server over the Unix socket. Hooks fire
//     this mode via type:"command" entries in the plugin's hooks.json.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/skitzo2000/ai-hardware-buddy/internal/ble"
	"github.com/skitzo2000/ai-hardware-buddy/internal/cli"
	"github.com/skitzo2000/ai-hardware-buddy/internal/mcpsrv"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(mcpsrv.Version)
			return
		case "serve":
			// Fall through to server mode.
		default:
			os.Exit(cli.Run(os.Args[1:]))
		}
	}

	runServer()
}

func runServer() {
	// Single-instance guard: if another hwbuddy serve is already listening
	// on the Unix socket, exit cleanly. Avoids the duplicate-daemon race
	// that crops up after /plugin uninstall + install + reload — Claude
	// Code can spawn a fresh stdio child before the previous one has
	// torn itself down, leaving two daemons fighting over the BLE adapter
	// and racing to handle MCP tool calls. Exit 0 so Claude Code doesn't
	// flag the new instance as crashed.
	if mcpsrv.AnotherInstanceRunning() {
		log.SetOutput(os.Stderr)
		log.Printf("another hwbuddy serve already owns %s — exiting", mcpsrv.SocketPath())
		os.Exit(0)
	}

	// stdout is the MCP JSON-RPC stream — log everything else. Default is
	// stderr, but Claude Code may not surface it; honour HWBUDDY_LOG so the
	// user can tail a file for debugging the reconnect loop, keepalive,
	// etc. When the env is unset, write to /tmp so the log is at a
	// predictable path on Linux/macOS.
	logTarget := os.Getenv("HWBUDDY_LOG")
	if logTarget == "" {
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			logTarget = rt + "/hwbuddy-mcp.log"
		} else {
			logTarget = "/tmp/hwbuddy-mcp.log"
		}
	}
	if f, err := os.OpenFile(logTarget, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		log.SetOutput(f)
	} else {
		log.SetOutput(os.Stderr)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	buddy := ble.New()
	if err := buddy.Enable(); err != nil {
		log.Fatalf("enable bluetooth adapter: %v", err)
	}

	srv := mcpsrv.New(buddy)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutdown signal received")
		_ = buddy.Disconnect()
		cancel()
	}()

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("mcp server: %v", err)
	}
}
