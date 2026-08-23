// Package tools implements the client-side Open Grok tool pack.
package tools

import (
	"context"
	"encoding/json"

	"github.com/megascan/open-grok/internal/goals"
	"github.com/megascan/open-grok/internal/types"
)

// Context is the per-session execution environment for a tool.
type Context struct {
	CWD            string
	SessionDir     string
	AlwaysApprove  bool
	PlanActive     bool
	PlanFile       string
	WorkspaceRoot  string
	SubagentDepth  int
	DispatchNested func(ctx context.Context, name string, args json.RawMessage) Result
	Goal           *goals.Tracker
}

// Result is a tool's model-facing output.
type Result struct {
	Content string
	IsError bool
}

// Tool is one client-executed function.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Access() types.AccessKind
	Execute(ctx context.Context, tc Context, args json.RawMessage) Result
}

// Specs returns JSON-schema tool definitions for the sampler.
func Specs(pack []Tool) []types.ToolSpec {
	out := make([]types.ToolSpec, 0, len(pack))
	for _, t := range pack {
		out = append(out, types.ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return out
}

func objectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func unmarshal(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func errResult(msg string) Result {
	return Result{Content: msg, IsError: true}
}

func okResult(msg string) Result {
	if msg == "" {
		msg = "(empty)"
	}
	return Result{Content: msg}
}
