package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

// Server is a configured MCP stdio server. The Go runtime can list and spawn
// servers; tool bridging is fail-open and must be combined with deny rules.
type Server struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// LoadServers reads [mcp_servers] from config.toml.
func LoadServers() []Server {
	cfg, err := config.Load()
	if err != nil || cfg.MCP == nil {
		return nil
	}
	var out []Server
	for name, s := range cfg.MCP {
		if s.Command == "" {
			continue
		}
		out = append(out, Server{Name: name, Command: s.Command, Args: s.Args, Env: s.Env})
	}
	return out
}

// Probe starts a stdio MCP server long enough to send initialize, then kills it.
// Failures are returned; they never escalate into a security boundary.
func Probe(ctx context.Context, s Server) error {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	init := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "open-grok", "version": "go"},
		},
	}
	enc := json.NewEncoder(stdin)
	if err := enc.Encode(init); err != nil {
		return err
	}
	_ = types.AccessMCP
	_ = strings.TrimSpace(s.Name)
	return fmt.Errorf("mcp probe: initialize sent to %s (stdio)", s.Name)
}
