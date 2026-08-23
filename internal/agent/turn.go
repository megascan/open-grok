package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/workspace"
)

const maxTurns = 32

// Event is a user-visible turn increment.
type Event struct {
	Kind    string
	Text    string
	Tool    string
	ToolID  string
	IsError bool
}

// Runtime is the injectable sampler/tool environment for a turn.
type Runtime struct {
	Model   catalog.Model
	Bearer  string
	HTTP    sampler.HTTPDoer
	Pack    []tools.Tool
	Approve func(tools.Tool, json.RawMessage) bool
}

// RunTurn samples until the model stops or maxTurns, dispatching tools.
func RunTurn(ctx context.Context, sess *Session, rt Runtime, userText string, emit func(Event)) error {
	if rt.Pack == nil {
		rt.Pack = tools.DefaultPack()
	}
	if len(sess.Messages) == 0 {
		prompt := SystemPrompt(rt.Model, sess.CWD, LoadProjectInstructions(sess.CWD))
		sess.Messages = append(sess.Messages, types.Message{Role: types.RoleSystem, Content: prompt})
	}
	sess.Messages = append(sess.Messages, types.Message{Role: types.RoleUser, Content: userText})
	_ = sess.AppendUpdate(map[string]any{"type": "user", "text": userText})
	sess.CloseExportBoundary(rt.Model.Provider)

	ad := sampler.Adapter{
		Provider: rt.Model.Provider,
		Backend:  rt.Model.APIBackend,
		BaseURL:  strings.TrimRight(rt.Model.BaseURL, "/"),
		Bearer:   rt.Bearer,
		HTTP:     rt.HTTP,
	}

	for turn := 0; turn < maxTurns; turn++ {
		req := types.SampleRequest{
			Model:     rt.Model.Model,
			Messages:  sess.Messages,
			Tools:     tools.Specs(rt.Pack),
			SessionID: sess.ID,
			TurnIndex: turn,
		}
		ch, err := ad.Stream(ctx, req)
		if err != nil {
			return err
		}
		var text strings.Builder
		var calls []types.ToolCall
		for ev := range ch {
			switch ev.Kind {
			case types.StreamDelta:
				text.WriteString(ev.Text)
				if emit != nil && ev.Text != "" {
					emit(Event{Kind: "text", Text: ev.Text})
				}
			case types.StreamThinking:
				if emit != nil && ev.Thinking != "" {
					emit(Event{Kind: "thinking", Text: ev.Thinking})
				}
			case types.StreamToolCall:
				if ev.ToolCall != nil {
					if ev.ToolCall.ID == "" {
						ev.ToolCall.ID = uuid.NewString()
					}
					calls = append(calls, *ev.ToolCall)
				}
			case types.StreamError:
				if ev.Err != nil {
					return ev.Err
				}
			}
		}
		assistant := types.Message{Role: types.RoleAssistant, Content: text.String(), ToolCalls: calls}
		sess.Messages = append(sess.Messages, assistant)
		if len(calls) == 0 {
			if sess.Title == "" {
				sess.Title = firstLine(userText)
			}
			return sess.Persist()
		}
		for _, call := range calls {
			if emit != nil {
				emit(Event{Kind: "tool", Tool: call.Name, ToolID: call.ID, Text: call.Arguments})
			}
			result := dispatchTool(ctx, sess, rt, call)
			sess.Messages = append(sess.Messages, types.Message{
				Role:       types.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    result.Content,
			})
			_ = sess.AppendUpdate(map[string]any{
				"type": "tool_result", "name": call.Name, "id": call.ID, "content": result.Content,
			})
			if emit != nil {
				emit(Event{Kind: "tool_result", Tool: call.Name, ToolID: call.ID, Text: result.Content, IsError: result.IsError})
			}
		}
	}
	return fmt.Errorf("max tool turns (%d) exceeded", maxTurns)
}

func dispatchTool(ctx context.Context, sess *Session, rt Runtime, call types.ToolCall) tools.Result {
	t := tools.Lookup(rt.Pack, call.Name)
	if t == nil {
		return tools.Result{Content: "unknown tool: " + call.Name, IsError: true}
	}
	if call.Name == "enter_plan_mode" {
		sess.Plan.Enter(filepath.Join(sess.Dir, "plan.md"))
	}
	if call.Name == "exit_plan_mode" {
		sess.Plan.Exit()
	}

	args := json.RawMessage(call.Arguments)
	if !json.Valid(args) {
		args = json.RawMessage(`{}`)
	}
	path := toolPath(sess.CWD, args)
	dec, reason := workspace.Check(workspace.Request{
		Tool:       t.Name(),
		Access:     t.Access(),
		Path:       path,
		Command:    toolCommand(args),
		AlwaysYOLO: sess.YOLO,
		PlanActive: sess.Plan.Active,
		PlanFile:   sess.Plan.PlanFile,
		AllowRules: sess.Allow,
		DenyRules:  sess.Deny,
	})
	if dec == workspace.Ask && rt.Approve != nil {
		if rt.Approve(t, args) {
			dec = workspace.Allow
		} else {
			dec = workspace.Deny
			reason = "user denied"
		}
	}
	if dec == workspace.Deny {
		return tools.Result{Content: "permission denied: " + reason, IsError: true}
	}
	if dec == workspace.Ask {
		return tools.Result{Content: "permission required for " + t.Name() + " (pass --always-approve to auto-approve)", IsError: true}
	}

	tc := tools.Context{
		CWD:           sess.CWD,
		SessionDir:    sess.Dir,
		AlwaysApprove: sess.YOLO,
		PlanActive:    sess.Plan.Active,
		PlanFile:      sess.Plan.PlanFile,
		WorkspaceRoot: sess.CWD,
	}
	return t.Execute(ctx, tc, args)
}

func toolPath(cwd string, args json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	p, _ := m["path"].(string)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

func toolCommand(args json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	s, _ := m["command"].(string)
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
