package pager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
)

type workflowPhase struct {
	Title  string
	Status string // pending | running | done | failed
}

type workflowAgent struct {
	ID     string
	Label  string
	Phase  string
	State  string // running | done | failed | paused
	Tokens int
}

type workflowRun struct {
	ID           string
	Name         string
	Kind         string
	Objective    string
	Status       string // active | user_paused | complete | failed | cancelled
	Started      time.Time
	Elapsed      time.Duration
	Phases       []workflowPhase
	Agents       []workflowAgent
	PauseMessage string
	Result       string
	Wire         string
	Builtin      bool
}

func (r workflowRun) isActive() bool { return r.Status == "active" }
func (r workflowRun) isTerminal() bool {
	switch r.Status {
	case "complete", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}
func (r workflowRun) canPause() bool  { return r.isActive() }
func (r workflowRun) canResume() bool { return r.Status == "user_paused" || r.Status == "failed" }
func (r workflowRun) canStop() bool   { return !r.isTerminal() }

func (r workflowRun) donePhases() int {
	n := 0
	for _, p := range r.Phases {
		if p.Status == "done" {
			n++
		}
	}
	return n
}

func (r workflowRun) doneAgents() int {
	n := 0
	for _, a := range r.Agents {
		if a.State != "running" {
			n++
		}
	}
	return n
}

func (r workflowRun) liveElapsed() time.Duration {
	if r.isActive() {
		return time.Since(r.Started)
	}
	if r.Elapsed > 0 {
		return r.Elapsed
	}
	return time.Since(r.Started)
}

func formatWorkflowElapsed(d time.Duration) string {
	ms := uint64(d / time.Millisecond)
	total := ms / 1000
	hours := total / 3600
	mins := (total % 3600) / 60
	secs := total % 60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%02ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

func workflowStatusGlyph(status string) string {
	switch status {
	case "active":
		return "●"
	case "complete":
		return "✓"
	case "failed", "interrupted":
		return "✗"
	case "cancelled":
		return "◌"
	default:
		return "⏸"
	}
}

func workflowAgentGlyph(state string) string {
	switch state {
	case "running":
		return "●"
	case "done":
		return "✓"
	case "failed":
		return "✗"
	default:
		return "◌"
	}
}

func builtinWorkflowPhases(kind string) []workflowPhase {
	switch kind {
	case "ultracode":
		return []workflowPhase{
			{Title: "Scout", Status: "running"},
			{Title: "Competing plans", Status: "pending"},
			{Title: "Implement", Status: "pending"},
			{Title: "Adversarial review", Status: "pending"},
		}
	case "deep-research":
		return []workflowPhase{
			{Title: "Plan questions", Status: "running"},
			{Title: "Gather claims", Status: "pending"},
			{Title: "Cross-check", Status: "pending"},
			{Title: "Report", Status: "pending"},
		}
	default:
		return []workflowPhase{{Title: "Run", Status: "running"}}
	}
}

func (m *model) selectedWorkflow() *workflowRun {
	if m.wfDetail >= 0 && m.wfDetail < len(m.workflows) {
		return &m.workflows[m.wfDetail]
	}
	if m.cursor >= 0 && m.cursor < len(m.workflows) {
		return &m.workflows[m.cursor]
	}
	return nil
}

func (m *model) openWorkflows() {
	m.overlay = overlayWorkflows
	m.cursor = 0
	m.wfDetail = -1
	if m.cursor >= len(m.workflows) {
		m.cursor = 0
	}
}

func (m *model) startWorkflow(kind, displayName, objective, wire string) tea.Cmd {
	id := fmt.Sprintf("wf-%d", time.Now().UnixNano()%1_000_000_000)
	agentID := "entropy"
	if m.sess != nil {
		agentID = m.sess.ID
	}
	run := workflowRun{
		ID:        id,
		Name:      displayName,
		Kind:      kind,
		Objective: objective,
		Status:    "active",
		Started:   time.Now(),
		Phases:    builtinWorkflowPhases(kind),
		Agents: []workflowAgent{{
			ID:    agentID,
			Label: "Entropy",
			Phase: builtinWorkflowPhases(kind)[0].Title,
			State: "running",
		}},
		Wire:    wire,
		Builtin: kind == "ultracode" || kind == "deep-research",
	}
	m.workflows = append(m.workflows, run)
	m.wfActiveID = id
	display := "/" + kind
	if objective != "" {
		display += " " + objective
	}
	return m.submitDisplay(display, wire)
}

func (m *model) finishWorkflow(err error) {
	id := m.wfActiveID
	m.wfActiveID = ""
	if id == "" {
		return
	}
	for i := range m.workflows {
		if m.workflows[i].ID != id {
			continue
		}
		r := &m.workflows[i]
		r.Elapsed = time.Since(r.Started)
		if r.Status != "active" {
			return
		}
		switch {
		case err == nil:
			r.Status = "complete"
			for j := range r.Phases {
				r.Phases[j].Status = "done"
			}
			for j := range r.Agents {
				r.Agents[j].State = "done"
			}
		case strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "cancelled"):
			r.Status = "cancelled"
			for j := range r.Agents {
				if r.Agents[j].State == "running" {
					r.Agents[j].State = "paused"
				}
			}
		default:
			r.Status = "failed"
			r.Result = err.Error()
			for j := range r.Agents {
				if r.Agents[j].State == "running" {
					r.Agents[j].State = "failed"
				}
			}
		}
		return
	}
}

func (m *model) pauseWorkflow(run *workflowRun) {
	if run == nil || !run.canPause() {
		return
	}
	run.Status = "user_paused"
	run.Elapsed = time.Since(run.Started)
	run.PauseMessage = "paused"
	for i := range run.Agents {
		if run.Agents[i].State == "running" {
			run.Agents[i].State = "paused"
		}
	}
	if m.wfActiveID == run.ID && m.turnCancel != nil {
		m.turnCancel()
	}
}

func (m *model) resumeWorkflow(run *workflowRun) tea.Cmd {
	if run == nil || !run.canResume() {
		return nil
	}
	run.Status = "active"
	run.Started = time.Now().Add(-run.Elapsed)
	run.PauseMessage = ""
	for i := range run.Agents {
		if run.Agents[i].State == "paused" || run.Agents[i].State == "failed" {
			run.Agents[i].State = "running"
		}
	}
	m.wfActiveID = run.ID
	prompt := "Resume the " + run.Name + " workflow from where it left off. Objective: " + run.Objective
	return m.submitDisplay("/workflow resume "+run.ID, prompt)
}

func (m *model) stopWorkflow(run *workflowRun) {
	if run == nil || !run.canStop() {
		return
	}
	run.Status = "cancelled"
	run.Elapsed = time.Since(run.Started)
	for i := range run.Agents {
		if run.Agents[i].State == "running" || run.Agents[i].State == "paused" {
			run.Agents[i].State = "paused"
		}
	}
	if m.wfActiveID == run.ID && m.turnCancel != nil {
		m.turnCancel()
	}
	m.wfActiveID = ""
}

func (m *model) handleWorkflow(arg string) tea.Cmd {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		m.openWorkflows()
		return nil
	}
	switch fields[0] {
	case "pause", "resume", "stop", "save":
		id := ""
		if len(fields) > 1 {
			id = fields[1]
		}
		run := m.findWorkflow(id)
		if run == nil && len(m.workflows) > 0 {
			run = &m.workflows[len(m.workflows)-1]
		}
		if run == nil {
			m.appendSys("no workflow run to " + fields[0])
			return nil
		}
		switch fields[0] {
		case "pause":
			m.pauseWorkflow(run)
		case "resume":
			return m.resumeWorkflow(run)
		case "stop":
			m.stopWorkflow(run)
		case "save":
			m.appendSys("workflow save is only available for non-builtin runs")
		}
		return nil
	}
	name := fields[0]
	rest := strings.Join(fields[1:], " ")
	for _, dir := range append(config.ProjectSubdir(m.opts.CWD, "workflows"), filepath.Join(config.Home(), "workflows")) {
		p := filepath.Join(dir, name)
		for _, ext := range []string{"", ".rhai", ".md"} {
			data, err := os.ReadFile(p + ext)
			if err != nil {
				continue
			}
			wire := "Launch saved workflow " + name + " with args " + rest + ".\nScript:\n" + string(data)
			return m.startWorkflow("workflow", name, rest, wire)
		}
	}
	wire := "Launch workflow " + name
	if rest != "" {
		wire += " " + rest
	}
	return m.startWorkflow("workflow", name, rest, wire)
}

func (m *model) findWorkflow(id string) *workflowRun {
	if id == "" {
		return nil
	}
	for i := range m.workflows {
		if m.workflows[i].ID == id || strings.EqualFold(m.workflows[i].Name, id) {
			return &m.workflows[i]
		}
	}
	return nil
}

func (m *model) handleWorkflowsKey(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	if m.wfDetail >= 0 {
		run := m.selectedWorkflow()
		switch s {
		case "esc", "left", "h", "tab":
			m.wfDetail = -1
			return nil
		case "up", "k":
			if run != nil && m.wfPhase > 0 {
				m.wfPhase--
			}
			return nil
		case "down", "j":
			if run != nil && m.wfPhase+1 < len(run.Phases) {
				m.wfPhase++
			}
			return nil
		case "p":
			m.pauseWorkflow(run)
			return nil
		case "r":
			return m.resumeWorkflow(run)
		case "x":
			m.stopWorkflow(run)
			return nil
		}
		return nil
	}
	switch s {
	case "esc":
		m.overlay = overlayNone
		return nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.workflows) {
			m.cursor++
		}
	case "enter":
		if len(m.workflows) > 0 {
			m.wfDetail = m.cursor
			m.wfPhase = 0
		}
	case "p":
		m.pauseWorkflow(m.selectedWorkflow())
	case "r":
		return m.resumeWorkflow(m.selectedWorkflow())
	case "x":
		m.stopWorkflow(m.selectedWorkflow())
	}
	return nil
}

func (m *model) renderWorkflows(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Workflows") + "\n\n")
	if len(m.workflows) == 0 {
		b.WriteString(dimStyle.Render("No workflow runs in this session yet.") + "\n\n")
		b.WriteString(dimStyle.Render("Start one with /ultracode <task>, /deep-research <query>, or ask for a workflow.") + "\n")
		b.WriteString("\n" + dimStyle.Render("esc close"))
		return modalBox.Width(w).Render(b.String())
	}
	if m.wfDetail >= 0 && m.wfDetail < len(m.workflows) {
		return modalBox.Width(w).Render(m.renderWorkflowDetail(w))
	}
	for i, run := range m.workflows {
		glyph := workflowStatusGlyph(run.Status)
		phasePart := run.Status
		if len(run.Phases) > 0 {
			phasePart = fmt.Sprintf("%d/%d phases", run.donePhases(), len(run.Phases))
		}
		meta := fmt.Sprintf("%s · %d/%d agents · %s", phasePart, run.doneAgents(), len(run.Agents), formatWorkflowElapsed(run.liveElapsed()))
		label := fmt.Sprintf("%s %s — %s", glyph, run.Name, truncate(run.Objective, 36))
		line := fmt.Sprintf("%s  %s", label, dimStyle.Render(meta))
		if i == m.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("enter detail  ·  p pause  ·  r resume  ·  x stop  ·  esc close"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) renderWorkflowDetail(w int) string {
	run := m.workflows[m.wfDetail]
	var b strings.Builder
	b.WriteString(titleStyle.Render("Workflows") + "\n\n")
	glyph := workflowStatusGlyph(run.Status)
	if run.isActive() {
		glyph = "●"
	}
	meta := fmt.Sprintf("%d/%d agents · %s", run.doneAgents(), len(run.Agents), formatWorkflowElapsed(run.liveElapsed()))
	b.WriteString(fmt.Sprintf("%s %s  %s\n", glyph, run.Name, dimStyle.Render(meta)))
	b.WriteString(dimStyle.Render(run.Objective) + "\n")
	if run.PauseMessage != "" {
		b.WriteString(errStyle.Render(run.PauseMessage) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("phases") + "\n")
	for i, p := range run.Phases {
		mark := workflowAgentGlyph(p.Status)
		if p.Status == "pending" {
			mark = "◌"
		}
		if p.Status == "running" {
			mark = "●"
		}
		line := fmt.Sprintf("  %s %s", mark, p.Title)
		if i == m.wfPhase {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("agents") + "\n")
	for _, a := range run.Agents {
		b.WriteString(fmt.Sprintf("  %s %s  %s\n", workflowAgentGlyph(a.State), a.Label, dimStyle.Render(a.State)))
	}
	var hints []string
	if run.canPause() {
		hints = append(hints, "p pause")
	}
	if run.canResume() {
		hints = append(hints, "r resume")
	}
	if run.canStop() {
		hints = append(hints, "x stop")
	}
	hints = append(hints, "←/tab runs", "esc close")
	b.WriteString("\n" + dimStyle.Render(strings.Join(hints, "  ·  ")))
	_ = w
	return b.String()
}
