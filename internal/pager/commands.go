package pager

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/version"
)

func (m *model) handleSlash(text string) tea.Cmd {
	fields := strings.Fields(text)
	cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	switch cmd {
	case "help":
		var b strings.Builder
		b.WriteString("Slash commands\n")
		for _, c := range slashCatalog {
			fmt.Fprintf(&b, "/%s  %s\n", c.Name, c.Help)
		}
		m.appendBlock(block{kind: blockSystem, body: b.String()})
	case "quit", "exit":
		return tea.Quit
	case "clear":
		m.blocks = nil
		m.vp.SetContent("")
	case "yolo":
		m.sess.YOLO = !m.sess.YOLO
		m.appendBlock(block{kind: blockSystem, body: fmt.Sprintf("always-approve = %v", m.sess.YOLO)})
	case "compact":
		_ = m.sess.AppendUpdate(map[string]any{"type": "auto_compact_started", "manual": true})
		m.appendBlock(block{kind: blockSystem, body: "compaction runs automatically when the transcript is large; a checkpoint was recorded"})
	case "plan":
		if m.sess.Plan.Active {
			m.sess.Plan.Exit()
			m.appendBlock(block{kind: blockSystem, body: "plan mode off"})
		} else {
			m.sess.Plan.Enter(m.sess.Plan.PlanFile)
			m.appendBlock(block{kind: blockSystem, body: "plan mode on — only " + m.sess.Plan.PlanFile + " may be edited"})
		}
	case "sessions":
		list, _ := agent.ListSessions(m.opts.CWD)
		var b strings.Builder
		for _, s := range list {
			fmt.Fprintf(&b, "%s  %s  %s\n", s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), s.Title)
		}
		if b.Len() == 0 {
			b.WriteString("(no sessions)")
		}
		m.appendBlock(block{kind: blockSystem, body: b.String()})
	case "export":
		m.appendBlock(block{kind: blockSystem, body: m.sess.ExportMarkdown()})
	case "memory":
		m.appendBlock(block{kind: blockSystem, body: memorySearch(arg)})
	case "mcp":
		cfg, _ := config.Load()
		if len(cfg.MCP) == 0 {
			m.appendBlock(block{kind: blockSystem, body: "(no mcp_servers)"})
			break
		}
		var b strings.Builder
		for name, s := range cfg.MCP {
			fmt.Fprintf(&b, "%s  %s\n", name, s.Command)
		}
		m.appendBlock(block{kind: blockSystem, body: b.String()})
	case "status":
		m.appendBlock(block{kind: blockSystem, body: fmt.Sprintf(
			"session %s\nmodel %s (%s)\nprovider %s\nyolo %v\nplan %v\n%s\nOpen Grok %s",
			m.sess.ID, m.opts.Model.ID, m.opts.Model.Name, m.opts.Model.Provider,
			m.sess.YOLO, m.sess.Plan.Active, m.sess.Goal.StatusLine(), version.String(),
		)})
	case "fork":
		dst, err := agent.ForkSession(m.sess)
		if err != nil {
			m.appendBlock(block{kind: blockSystem, body: err.Error(), err: true})
			break
		}
		m.sess = dst
		m.appendBlock(block{kind: blockSystem, body: "forked to " + dst.ID})
	case "rewind":
		if len(m.sess.Messages) > 2 {
			m.sess.Messages = m.sess.Messages[:len(m.sess.Messages)-2]
			_ = m.sess.Persist()
			m.appendBlock(block{kind: blockSystem, body: "rewound last exchange"})
		}
	case "model":
		if arg == "" {
			m.appendBlock(block{kind: blockSystem, body: "current model: " + m.opts.Model.ID + " (" + m.opts.Model.Provider.DisplayName() + ")"})
			break
		}
		mod, ok := catalog.Lookup(arg)
		if !ok {
			m.appendBlock(block{kind: blockSystem, body: "unknown model " + arg, err: true})
			break
		}
		m.opts.Model = mod
		m.sess.ModelID = mod.ID
		m.sess.Provider = mod.Provider
		m.sess.Backend = mod.APIBackend
		m.sess.CloseExportBoundary(mod.Provider)
		m.appendBlock(block{kind: blockSystem, body: "switched to " + mod.Name + " via " + mod.Provider.DisplayName()})
	case "login":
		m.appendBlock(block{kind: blockSystem, body: "use `open-grok login [provider]` from a TTY to store credentials under ~/.opengrok — never ~/.grok"})
	case "goal":
		return m.handleGoal(arg)
	default:
		m.appendBlock(block{kind: blockSystem, body: "unknown command /" + cmd + "  ·  /help", err: true})
	}
	m.refreshVP()
	return nil
}

func (m *model) handleGoal(arg string) tea.Cmd {
	gs := parseGoalSlash(arg)
	switch gs.Verb {
	case goalVerbStatus:
		m.appendBlock(block{kind: blockGoal, title: "status", body: m.goalStatusText()})
	case goalVerbPause:
		if m.sess.Goal.PauseUser() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "user_paused"})
			m.appendBlock(block{kind: blockGoal, body: "goal paused"})
		} else {
			m.appendBlock(block{kind: blockGoal, body: "nothing to pause (goal is not Active)", err: true})
		}
	case goalVerbResume:
		if m.sess.Goal.Resume() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "active"})
			m.appendBlock(block{kind: blockGoal, body: "goal resumed — Active"})
			m.refreshVP()
			return m.submit("Continue the active goal. Call update_goal as you make progress.")
		}
		m.appendBlock(block{kind: blockGoal, body: "nothing to resume", err: true})
	case goalVerbComplete:
		if m.sess.Goal.UserComplete() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "complete"})
			m.appendBlock(block{kind: blockGoal, body: "goal marked complete (user-verified)"})
		} else {
			m.appendBlock(block{kind: blockGoal, body: "no goal to complete", err: true})
		}
	case goalVerbClear:
		m.sess.Goal.Clear()
		_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "cleared"})
		m.appendBlock(block{kind: blockGoal, body: "goal cleared"})
	case goalVerbStart:
		if gs.Objective == "" {
			m.appendBlock(block{kind: blockGoal, body: m.goalStatusText()})
			break
		}
		if err := m.sess.Goal.Create(gs.Objective, gs.Budget); err != nil {
			m.appendBlock(block{kind: blockGoal, body: err.Error(), err: true})
			break
		}
		_ = m.sess.AppendUpdate(map[string]any{
			"type": "goal_mode_state", "status": "active", "objective": gs.Objective,
		})
		m.appendBlock(block{kind: blockGoal, body: "Active goal:\n" + gs.Objective + "\n\nThe model reports progress with update_goal. Completion is a claim until you run /goal complete."})
		m.refreshVP()
		return m.submit("Begin the active goal. Inspect the workspace, make progress, and call update_goal after meaningful steps.")
	}
	m.refreshVP()
	return nil
}

func (m *model) goalStatusText() string {
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		return "No goal. Start one with /goal <objective> [--budget <tokens>]."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\nphase: %s\nid: %s\n\n%s", snap.Status, snap.Phase, snap.GoalID, snap.Objective)
	if snap.CompletionClaimed {
		fmt.Fprintf(&b, "\n\ncompletion claim pending /goal complete:\n%s", snap.CompletionSummary)
	}
	if snap.PauseMessage != "" {
		fmt.Fprintf(&b, "\n\npause: %s", snap.PauseMessage)
	}
	return b.String()
}

func memorySearch(q string) string {
	body, _ := json.Marshal(map[string]string{"query": q})
	return tools.MemorySearch{}.Execute(context.Background(), tools.Context{}, body).Content
}
