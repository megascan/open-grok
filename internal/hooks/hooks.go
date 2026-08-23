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
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/config"
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
	ev := Event{Hook: "PreToolUse", Tool: tool, CWD: cwd, Session: sessionDir, ArgsJSON: args}
	for _, dir := range hookDirs(cwd) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "PreToolUse")
		code, out, err := runHook(ctx, path, ev)
		if err != nil {
			continue
		}
		if code == 2 {
			return true, "hook denied: " + out
		}
	}
	for _, dir := range hookDirs(cwd) {
		if deny, reason := runImportedJSON(ctx, dir, ev); deny {
			return true, reason
		}
	}
	return false, ""
}

func hookDirs(cwd string) []string {
	dirs := config.ProjectSubdir(cwd, "hooks")
	if home := config.FirstEnv(config.HomeEnv(), "OPENGROK_HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, "hooks"))
		return dirs
	}
	if user, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(user, ".entropy", "hooks"), filepath.Join(user, ".opengrok", "hooks"))
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

func runImportedJSON(ctx context.Context, dir string, ev Event) (bool, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "imported-from-claude.json"))
	if err != nil {
		return false, ""
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return false, ""
	}
	hooks, _ := root["hooks"].(map[string]any)
	groups, _ := hooks[ev.Hook].([]any)
	if len(groups) == 0 {
		// Claude events are PreToolUse; also try case variants.
		groups, _ = hooks["PreToolUse"].([]any)
	}
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		if gm == nil {
			continue
		}
		matcher, _ := gm["matcher"].(string)
		if matcher != "" && matcher != "*" && !strings.EqualFold(matcher, ev.Tool) {
			continue
		}
		handlers, _ := gm["hooks"].([]any)
		for _, h := range handlers {
			hm, _ := h.(map[string]any)
			if hm == nil {
				continue
			}
			cmd, _ := hm["command"].(string)
			if cmd == "" {
				continue
			}
			code, out, err := runCommandHook(ctx, cmd, ev)
			if err != nil {
				continue
			}
			if code == 2 {
				return true, "hook denied: " + out
			}
		}
	}
	return false, ""
}

func runCommandHook(ctx context.Context, command string, ev Event) (int, string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	body, _ := json.Marshal(ev)
	cmd.Stdin = bytes.NewReader(body)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil
		}
	}
	return code, buf.String(), err
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

// ImportedHook is one Claude-imported command hook from imported-from-claude.json.
type ImportedHook struct {
	Event   string
	Matcher string
	Command string
	Path    string
}

// ListImported walks hook dirs for imported-from-claude.json entries.
func ListImported(cwd string) []ImportedHook {
	var out []ImportedHook
	for _, dir := range hookDirs(cwd) {
		path := filepath.Join(dir, "imported-from-claude.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			continue
		}
		hooks, _ := root["hooks"].(map[string]any)
		for event, groupsAny := range hooks {
			groups, _ := groupsAny.([]any)
			for _, g := range groups {
				gm, _ := g.(map[string]any)
				if gm == nil {
					continue
				}
				matcher, _ := gm["matcher"].(string)
				if matcher == "" {
					matcher = "*"
				}
				handlers, _ := gm["hooks"].([]any)
				for _, h := range handlers {
					hm, _ := h.(map[string]any)
					if hm == nil {
						continue
					}
					cmd, _ := hm["command"].(string)
					if cmd == "" {
						continue
					}
					out = append(out, ImportedHook{Event: event, Matcher: matcher, Command: cmd, Path: path})
				}
			}
		}
	}
	return out
}
