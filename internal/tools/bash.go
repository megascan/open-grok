package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// Bash runs a shell command in the session workspace.
type Bash struct{}

func (Bash) Name() string { return "bash" }
func (Bash) Description() string {
	return "Run a shell command in the workspace. Use for builds, tests, git, and inspection. Prefer dedicated file tools for reading or editing."
}
func (Bash) Access() types.AccessKind { return types.AccessBash }
func (Bash) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"command":     map[string]any{"type": "string", "description": "Shell command to run"},
		"working_dir": map[string]any{"type": "string", "description": "Optional working directory"},
		"timeout_ms":  map[string]any{"type": "integer", "description": "Timeout in milliseconds (default 120000)"},
	}, []string{"command"})
}

func (Bash) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	command := str(m["command"])
	if command == "" {
		return errResult("bash: command is required")
	}
	dir := str(m["working_dir"])
	if dir == "" {
		dir = tc.CWD
	}
	timeout := 120 * time.Second
	if n, ok := m["timeout_ms"].(float64); ok && n > 0 {
		timeout = time.Duration(n) * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd.exe", "/C", command)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.CommandContext(cctx, shell, "-c", command)
	}
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var b strings.Builder
	if stdout.Len() > 0 {
		b.Write(stdout.Bytes())
	}
	if stderr.Len() > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(stderr.String())
	}
	out := capOutput(b.String(), 200_000)
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return errResult("bash: timed out\n" + out)
		}
		return errResult(err.Error() + "\n" + out)
	}
	return okResult(out)
}

func capOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… truncated …"
}
