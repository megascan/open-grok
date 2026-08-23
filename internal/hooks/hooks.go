// Package hooks runs PreToolUse / PostToolUse scripts and fails open.
//
// Hooks are not a security boundary; combine them with deny rules and sandbox.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Event is the JSON payload posted to a hook script on stdin.
type Event struct {
	Hook     string `json:"hook"`
	Tool     string `json:"tool"`
	CWD      string `json:"cwd"`
	Session  string `json:"session"`
	ArgsJSON string `json:"arguments"`
}

// PreToolUse looks for executable hooks and runs them. A hook that exits 2
// denies the tool; any other failure (missing, crash, timeout) is ignored.
func PreToolUse(ctx context.Context, cwd, sessionDir, tool, args string) (deny bool, reason string) {
	for _, dir := range hookDirs(cwd) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "PreToolUse")
		code, out, err := runHook(ctx, path, Event{Hook: "PreToolUse", Tool: tool, CWD: cwd, Session: sessionDir, ArgsJSON: args})
		if err != nil {
			continue
		}
		if code == 2 {
			return true, "hook denied: " + out
		}
	}
	return false, ""
}

func hookDirs(cwd string) []string {
	dirs := []string{filepath.Join(cwd, ".opengrok", "hooks")}
	if home := os.Getenv("OPENGROK_HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, "hooks"))
	}
	return dirs
}

// Dirs returns project then user hook directories.
func Dirs(cwd string) []string { return hookDirs(cwd) }

// List names executable hook scripts discovered on disk.
func List(cwd string) []string {
	var out []string
	for _, dir := range hookDirs(cwd) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func runHook(ctx context.Context, path string, ev Event) (int, string, error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return 0, "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, path)
	body, _ := json.Marshal(ev)
	cmd.Stdin = bytes.NewReader(body)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil
		}
	}
	return code, buf.String(), err
}
