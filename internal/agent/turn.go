package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/codemode"
	"github.com/megascan/open-grok/internal/hooks"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/workspace"
)

const maxTurns = 32

// Event is a user-visible turn increment.
type Event struct {
	Kind         string
	Text         string
	Tool         string
	ToolID       string
	IsError      bool
	Percent      uint8
	TokensBefore uint64
	TokensAfter  uint64
	ElapsedMS    int64
}

// Runtime is the injectable sampler/tool environment for a turn.
type Runtime struct {
	Model   catalog.Model
	Bearer  string
	HTTP    sampler.HTTPDoer
	Pack    []tools.Tool
	Approve func(tools.Tool, json.RawMessage) bool
	AskUser func(question string, options []string) string
	AskPlan func(plan string) string
}

// RunTurn samples until the model stops or maxTurns, dispatching tools.
func RunTurn(ctx context.Context, sess *Session, rt Runtime, userText string, emit func(Event)) error {
	ensureRuntime(sess, rt)
	sess.EnsureMCP(ctx)
	sess.Approve = rt.Approve
	sess.AskUser = rt.AskUser
	sess.AskPlan = rt.AskPlan
	if !rt.Model.ToolsEnabled() {
		rt.Pack = nil
	} else {
		if rt.Pack == nil {
			rt.Pack = PackFor(rt.Model, sess.JS)
		}
		if len(sess.MCPTools) > 0 {
			rt.Pack = append(rt.Pack, sess.MCPTools...)
		}
		rt.Pack = bindTask(sess, rt)
		rt.Pack = bindCollab(sess, rt)
	}
	if len(sess.Messages) == 0 {
		prompt := SystemPrompt(rt.Model, sess.CWD, LoadProjectInstructions(sess.CWD))
		sess.Messages = append(sess.Messages, types.Message{Role: types.RoleSystem, Content: prompt})
	}
	for _, extra := range drainInbox(sess) {
		sess.Messages = append(sess.Messages, extra)
	}
	if reminder := sess.Goal.Reminder(); reminder != "" {
		userText = reminder + "\n" + userText
	}
	sess.Messages = append(sess.Messages, types.Message{Role: types.RoleUser, Content: userText})
	_ = sess.AppendUpdate(map[string]any{"type": "user", "text": userText})
	sess.CloseExportBoundary(rt.Model.Provider)
	if st := compact(sess); st.Ran && emit != nil {
		win := rt.Model.ContextWindow
		if win <= 0 {
			win = 128_000
		}
		beforeTok := uint64(st.BytesBefore / 4)
		afterTok := uint64(st.BytesAfter / 4)
		pct := 0
		if win > 0 {
			pct = int(beforeTok) * 100 / win
			if pct > 99 {
				pct = 99
			}
		}
		emit(Event{Kind: "compact_started", Percent: uint8(pct)})
		emit(Event{Kind: "compact_completed", TokensBefore: beforeTok, TokensAfter: afterTok, ElapsedMS: st.Elapsed.Milliseconds()})
	}

	ad := sampler.Adapter{
		Provider: rt.Model.Provider,
		Backend:  rt.Model.APIBackend,
		BaseURL:  strings.TrimRight(rt.Model.BaseURL, "/"),
		Bearer:   rt.Bearer,
		HTTP:     rt.HTTP,
	}

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req := types.SampleRequest{
			Model:     rt.Model.Model,
			Messages:  sess.Messages,
			Tools:     tools.Specs(rt.Pack),
			SessionID: sess.ID,
			TurnIndex: turn,
		}
		ch, err := ad.Stream(ctx, req)
		if err != nil && len(req.Tools) > 0 && looksLikeNoToolSupport(err) {
			rt.Pack = nil
			req.Tools = nil
			off := false
			rt.Model.SupportsTools = &off
			ch, err = ad.Stream(ctx, req)
		}
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
			if err := ctx.Err(); err != nil {
				return err
			}
			if emit != nil && call.Name != "exec" && call.Name != "wait" {
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
			if emit != nil && call.Name != "exec" && call.Name != "wait" {
				emit(Event{Kind: "tool_result", Tool: call.Name, ToolID: call.ID, Text: result.Content, IsError: result.IsError})
			}
		}
	}
	return fmt.Errorf("max tool turns (%d) exceeded", maxTurns)
}

func ensureRuntime(sess *Session, rt Runtime) {
	mode := EffectiveToolMode(rt.Model)
	if mode != types.ToolModeCodeMode && mode != types.ToolModeCodeModeOnly {
		return
	}
	if sess.JS != nil {
		return
	}
	host := tools.Context{CWD: sess.CWD, SessionDir: sess.Dir, AlwaysApprove: sess.YOLO, PlanActive: sess.Plan.Active, PlanFile: sess.Plan.PlanFile, WorkspaceRoot: sess.CWD, SubagentDepth: sess.SubagentDepth, Goal: sess.Goal}
	host.DispatchNested = func(ctx context.Context, name string, args json.RawMessage) tools.Result {
		t := tools.Lookup(tools.NestedPack(), name)
		if t == nil {
			return tools.Result{Content: "unknown nested tool " + name, IsError: true}
		}
		return dispatchPrepared(ctx, sess, t, args)
	}
	sess.JS = codemode.New(host, tools.NestedPack())
}

func bindTask(sess *Session, rt Runtime) []tools.Tool {
	out := make([]tools.Tool, 0, len(rt.Pack))
	for _, t := range rt.Pack {
		if ts, ok := t.(tools.TaskSpawn); ok {
			parent := sess
			model := rt.Model
			bearer := rt.Bearer
			httpDo := rt.HTTP
			ts.Spawn = func(ctx context.Context, prompt, label string) tools.Result {
				if parent.SubagentDepth >= 1 {
					return tools.Result{Content: "nested subagents are unsupported (MAX_SUBAGENT_DEPTH = 1)", IsError: true}
				}
				child, err := NewSession(parent.CWD, model, parent.YOLO)
				if err != nil {
					return tools.Result{Content: err.Error(), IsError: true}
				}
				child.YOLO = parent.YOLO
				child.Allow = parent.Allow
				child.Deny = parent.Deny
				child.ExtraDirs = append([]string(nil), parent.ExtraDirs...)
				child.Approve = parent.Approve
				child.AskUser = parent.AskUser
				child.AskPlan = parent.AskPlan
				child.PermMode = parent.PermMode
				child.Plan = workspace.NewInactive()
				child.Plan.PlanFile = filepath.Join(child.Dir, "plan.md")
				child.SubagentDepth = parent.SubagentDepth + 1
				if parent.Team != nil {
					parent.Team.Register(child.ID, label)
					if parent.Children == nil {
						parent.Children = map[string]*Session{}
					}
					parent.Children[child.ID] = child
				}
				var b strings.Builder
				err = RunTurn(ctx, child, Runtime{
					Model: model, Bearer: bearer, HTTP: httpDo,
					Approve: parent.Approve, AskUser: parent.AskUser, AskPlan: parent.AskPlan,
				}, prompt, func(ev Event) {
					if ev.Kind == "text" {
						b.WriteString(ev.Text)
					}
				})
				sum := b.String()
				if sum == "" {
					sum = "subagent " + child.ID + " finished"
				}
				if label != "" {
					sum = label + ": " + sum
				}
				if parent.Team != nil {
					parent.Team.Finish(child.ID, sum, err != nil)
				}
				if err != nil {
					return tools.Result{Content: err.Error(), IsError: true}
				}
				return tools.Result{Content: sum}
			}
			out = append(out, ts)
			continue
		}
		out = append(out, t)
	}
	return out
}

func bindCollab(sess *Session, rt Runtime) []tools.Tool {
	out := make([]tools.Tool, 0, len(rt.Pack))
	for _, t := range rt.Pack {
		switch v := t.(type) {
		case tools.ListAgents:
			v.Box = sess.Team
			out = append(out, v)
		case tools.SendMessage:
			v.Box = sess.Team
			out = append(out, v)
		case tools.FollowupTask:
			v.Box = sess.Team
			if sess.Team != nil {
				parent := sess
				sess.Team.SetFollowup(func(ctx context.Context, id, prompt string) tools.Result {
					child := parent.Children[id]
					if child == nil {
						return tools.Result{Content: "unknown agent id", IsError: true}
					}
					var b strings.Builder
					model, ok := catalog.Lookup(child.ModelID)
					if !ok {
						model = catalog.Model{ID: child.ModelID, Model: child.ModelID, Provider: child.Provider, APIBackend: child.Backend}
					}
					err := RunTurn(ctx, child, Runtime{Model: model, Bearer: rt.Bearer, HTTP: rt.HTTP}, prompt, func(ev Event) {
						if ev.Kind == "text" {
							b.WriteString(ev.Text)
						}
					})
					if err != nil {
						return tools.Result{Content: err.Error(), IsError: true}
					}
					return tools.Result{Content: b.String()}
				})
			}
			out = append(out, v)
		case tools.WaitAgent:
			v.Box = sess.Team
			out = append(out, v)
		case tools.MessageSession:
			v.SourceID = sess.ID
			v.SourceProject = filepath.Base(sess.CWD)
			out = append(out, v)
		default:
			out = append(out, t)
		}
	}
	return out
}

func dispatchTool(ctx context.Context, sess *Session, rt Runtime, call types.ToolCall) tools.Result {
	t := tools.Lookup(rt.Pack, call.Name)
	if t == nil {
		return tools.Result{Content: "unknown tool: " + call.Name, IsError: true}
	}
	args := json.RawMessage(call.Arguments)
	if !json.Valid(args) {
		args = json.RawMessage(`{}`)
	}
	return dispatchPrepared(ctx, sess, t, args)
}

func dispatchPrepared(ctx context.Context, sess *Session, t tools.Tool, args json.RawMessage) tools.Result {
	if deny, reason := hooks.PreToolUse(ctx, sess.CWD, sess.Dir, t.Name(), string(args)); deny {
		return tools.Result{Content: reason, IsError: true}
	}
	path := toolPath(sess.CWD, args)
	dec, reason := workspace.Check(workspace.Request{
		Tool:              t.Name(),
		Access:            t.Access(),
		Path:              path,
		Command:           toolCommand(args),
		AlwaysYOLO:        sess.YOLO,
		AllowEditsSession: sess.AllowEditsSession,
		AutoMode:          sess.PermMode == "auto",
		PlanActive:        sess.Plan.Active,
		PlanFile:          sess.Plan.PlanFile,
		AllowRules:        sess.Allow,
		DenyRules:         sess.Deny,
		ExtraRoots:        sess.ExtraDirs,
	})
	if dec == workspace.Deny {
		return tools.Result{Content: "permission denied: " + reason, IsError: true}
	}
	if dec == workspace.Ask {
		if sess.Approve == nil {
			return tools.Result{Content: "permission required for " + t.Name() + " (pass --always-approve to auto-approve)", IsError: true}
		}
		if !sess.Approve(t, args) {
			return tools.Result{Content: "permission denied: user rejected " + t.Name(), IsError: true}
		}
	}
	if t.Name() == "enter_plan_mode" {
		if !sess.YOLO && sess.Approve != nil && !sess.Approve(t, args) {
			return tools.Result{Content: "permission denied: user rejected " + t.Name(), IsError: true}
		}
		sess.Plan.Enter(filepath.Join(sess.Dir, "plan.md"))
	}
	if t.Name() == "exit_plan_mode" {
		return finishPlanMode(sess)
	}
	tc := tools.Context{
		CWD:           sess.CWD,
		SessionDir:    sess.Dir,
		AlwaysApprove: sess.YOLO,
		PlanActive:    sess.Plan.Active,
		PlanFile:      sess.Plan.PlanFile,
		WorkspaceRoot: sess.CWD,
		SubagentDepth: sess.SubagentDepth,
		Goal:          sess.Goal,
		AskUser:       sess.AskUser,
	}
	return t.Execute(ctx, tc, args)
}

const emptyPlanPlaceholder = `# No plan written yet

The agent exited plan mode without writing a plan.

- **Approve** — leave plan mode and start implementing
- **Request changes** — send the agent back to planning
- **Quit** — abandon and turn plan mode off
`

func finishPlanMode(sess *Session) tools.Result {
	body := ""
	if sess.Plan.PlanFile != "" {
		if data, err := os.ReadFile(sess.Plan.PlanFile); err == nil {
			body = string(data)
		}
	}
	if strings.TrimSpace(body) == "" {
		body = emptyPlanPlaceholder
	}
	decision := "approve"
	if sess.AskPlan != nil {
		decision = sess.AskPlan(body)
	}
	switch {
	case decision == "quit":
		sess.Plan.Exit()
		return tools.Result{Content: "plan mode turned off without approving"}
	case strings.HasPrefix(decision, "changes:"):
		return tools.Result{Content: "The user requested changes to the plan:\n" + strings.TrimPrefix(decision, "changes:")}
	default:
		sess.Plan.Exit()
		msg := "plan approved; start implementing"
		rest := strings.TrimPrefix(decision, "approve:")
		if rest != "" && rest != "approve" && rest != decision {
			msg += "\n" + rest
		} else if strings.HasPrefix(decision, "approve:") && len(decision) > len("approve:") {
			msg += "\n" + strings.TrimPrefix(decision, "approve:")
		}
		return tools.Result{Content: msg}
	}
}

type compactStats struct {
	Ran         bool
	BytesBefore int
	BytesAfter  int
	Elapsed     time.Duration
}

func compact(s *Session) compactStats {
	return compactSession(s, false, "")
}

func compactSession(s *Session, force bool, note string) compactStats {
	start := time.Now()
	var n int
	for _, m := range s.Messages {
		n += len(m.Content)
	}
	if !force && (n < 120_000 || len(s.Messages) < 8) {
		return compactStats{}
	}
	_ = s.AppendUpdate(map[string]any{"type": "auto_compact_started", "bytes": n, "manual": force, "note": note})
	keepN := 6
	if len(s.Messages) < keepN+1 {
		keepN = len(s.Messages)
	}
	keep := s.Messages[len(s.Messages)-keepN:]
	summary := "Earlier turns were compacted. Continue from the recent tail."
	if note != "" {
		summary += " Keep: " + note
	}
	head := []types.Message{}
	if len(s.Messages) > 0 {
		head = append(head, s.Messages[0])
	}
	s.Messages = append(head, types.Message{Role: types.RoleSystem, Content: summary})
	s.Messages = append(s.Messages, keep...)
	after := 0
	for _, m := range s.Messages {
		after += len(m.Content)
	}
	_ = s.AppendUpdate(map[string]any{"type": "auto_compact_completed", "kept": len(s.Messages)})
	_ = s.Persist()
	return compactStats{Ran: true, BytesBefore: n, BytesAfter: after, Elapsed: time.Since(start)}
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

func looksLikeNoToolSupport(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tool") &&
		(strings.Contains(s, "no endpoints") ||
			strings.Contains(s, "not support") ||
			strings.Contains(s, "unsupported") ||
			strings.Contains(s, "does not support"))
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

// ExportMarkdown renders the session transcript.
func (s *Session) ExportMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Title)
	fmt.Fprintf(&b, "- id: `%s`\n- model: `%s` (%s)\n\n", s.ID, s.ModelID, s.Provider)
	for _, m := range s.Messages {
		switch m.Role {
		case types.RoleUser:
			b.WriteString("## User\n\n")
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		case types.RoleAssistant:
			b.WriteString("## Entropy\n\n")
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
