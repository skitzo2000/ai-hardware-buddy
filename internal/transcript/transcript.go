// Package transcript parses Claude Code session JSONL files for token usage.
//
// Each assistant record in the transcript carries a `message.usage` block
// with input_tokens, output_tokens, cache_creation_input_tokens and
// cache_read_input_tokens. We sum input + output + cache_creation as
// "work done by the model"; cache_read is effectively free.
//
// Three windows are exposed:
//   - lifetime (all records in the file)
//   - today    (records with a UTC timestamp on today's date)
//   - window5h (records within the last 5h, plus when that window resets)
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const WindowHours = 5

// Usage rolls up tokens across the three windows.
type Usage struct {
	Lifetime         uint32
	Today            uint32
	Window5h         uint32
	WindowStartedAt  *time.Time
	WindowResetsInS  int
}

type rawRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Usage struct {
			InputTokens              uint32 `json:"input_tokens"`
			OutputTokens             uint32 `json:"output_tokens"`
			CacheCreationInputTokens uint32 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Summarize reads a transcript JSONL and returns aggregated token usage.
// Corrupt JSON lines and records without usage are silently skipped.
func Summarize(path string, now time.Time) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	todayDate := now.UTC().Format("2006-01-02")
	windowFloor := now.Add(-time.Hour * WindowHours)

	var (
		lifetime, today, window uint32
		windowFirst             *time.Time
	)

	sc := bufio.NewScanner(f)
	// Claude Code transcript lines can be large (system prompts, big tool
	// outputs). Bump the scanner buffer well above the 64KB default.
	buf := make([]byte, 0, 1<<20)
	sc.Buffer(buf, 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r rawRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Type != "assistant" {
			continue
		}
		u := r.Message.Usage
		toks := u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens
		if toks == 0 {
			continue
		}
		lifetime += toks

		ts, err := parseTimestamp(r.Timestamp)
		if err != nil {
			continue
		}
		if ts.UTC().Format("2006-01-02") == todayDate {
			today += toks
		}
		if !ts.Before(windowFloor) {
			window += toks
			if windowFirst == nil || ts.Before(*windowFirst) {
				t := ts
				windowFirst = &t
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Usage{}, err
	}

	var resetIn int
	if windowFirst != nil {
		reset := windowFirst.Add(time.Hour * WindowHours)
		if reset.After(now) {
			resetIn = int(reset.Sub(now).Seconds())
		}
	}

	return Usage{
		Lifetime:        lifetime,
		Today:           today,
		Window5h:        window,
		WindowStartedAt: windowFirst,
		WindowResetsInS: resetIn,
	}, nil
}

// FindCurrentTranscript locates the most recently modified JSONL in Claude
// Code's per-project transcript directory for `cwd`. Returns "" if no
// transcripts are present.
func FindCurrentTranscript(home, cwd string) string {
	if home == "" || cwd == "" {
		return ""
	}
	slug := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type fileInfo struct {
		path string
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path: filepath.Join(dir, e.Name()),
			mod:  info.ModTime(),
		})
	}
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files[0].path
}

func parseTimestamp(ts string) (time.Time, error) {
	if ts == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	// Claude Code emits ISO 8601 with trailing Z.
	return time.Parse(time.RFC3339Nano, ts)
}
