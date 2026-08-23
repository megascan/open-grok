package pager

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	lifeQuoteL = "\u201c"
	lifeQuoteR = "\u201d"
	lifeAngle  = " \u203a "
	lifeTimes  = "\u00d7"
	lifeBranch = "  \u251c\u2500"
	lifeCorner = "  \u2514\u2500"
)

type subagentCard struct {
	Desc, ChildID, SubType, Persona, Role, Model string
	Background                                   bool
	Status                                       string // running | completed | failed | cancelled
	Elapsed                                      time.Duration
	Activity, Err                                string
}

type swarmMember struct {
	Index               int
	Item, Desc, ChildID string
	Status              string // queued | running | waiting | completed | failed | cancelled
	Turns, Tools        int
	Duration            time.Duration
	Activity            string
}

type swarmCard struct {
	ID, Desc string
	Expected int
	Members  []swarmMember
	Detached bool
}

type bgTaskCard struct {
	Command, TaskID, Desc string
	Status                string // started | completed | failed | killed
	Elapsed               time.Duration
	ExitCode              int
	HasExit               bool
	Signal                string
}

func quotedDesc(desc string, maxW int) string {
	if maxW <= 2 {
		return lifeQuoteL + "\u2026" + lifeQuoteR
	}
	inner := truncate(desc, maxW-2)
	return lifeQuoteL + inner + lifeQuoteR
}

func formatSubagentMeta(persona, role, model string) string {
	var parts []string
	if persona != "" {
		parts = append(parts, persona)
	}
	if role != "" && !strings.EqualFold(role, persona) {
		parts = append(parts, role)
	}
	if model != "" {
		parts = append(parts, model)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (m *model) renderSubagentBlock(bl block, w int) string {
	c := bl.subagent
	if c == nil {
		return ""
	}
	ident := toolStyle.Bold(true).Render("Subagent")
	rest := ""
	if c.SubType != "" {
		rest += toolStyle.Render(" [" + c.SubType + "]")
	}
	rest += toolStyle.Render(lifeAngle)
	verb := "running: "
	if c.Background && c.Status == "running" {
		verb = "started: "
	}
	switch c.Status {
	case "completed":
		verb = "completed in " + formatDuration(c.Elapsed) + ": "
	case "failed":
		detail := ""
		if c.Err != "" {
			detail = " (" + c.Err + ")"
		}
		verb = "failed in " + formatDuration(c.Elapsed) + detail + ": "
	case "cancelled":
		verb = "cancelled in " + formatDuration(c.Elapsed) + ": "
	}
	activity := ""
	if c.Status == "running" && strings.TrimSpace(c.Activity) != "" {
		activity = " " + emDash + " " + c.Activity
	}
	meta := formatSubagentMeta(c.Persona, c.Role, c.Model)
	overhead := 20 + len(verb) + len(activity) + len(meta)
	if c.SubType != "" {
		overhead += len(c.SubType) + 3
	}
	desc := quotedDesc(c.Desc, max(8, w-overhead))
	line := ident + rest + dimStyle.Render(verb+desc+activity+meta)
	var bar lipgloss.Style
	switch {
	case c.Status == "running" && m.busy:
		bar = toolStyle
	case c.Status == "completed":
		bar = okStyle
	case c.Status == "failed" || c.Status == "cancelled":
		bar = errStyle
	default:
		return line
	}
	var b strings.Builder
	writeAccentWith(&b, bar, line)
	return strings.TrimRight(b.String(), "\n")
}

func (c *swarmCard) counts() (done, failed, cancelled, running, waiting, queued int) {
	for _, m := range c.Members {
		switch m.Status {
		case "completed":
			done++
		case "failed":
			failed++
		case "cancelled":
			cancelled++
		case "running":
			running++
		case "waiting":
			waiting++
		default:
			queued++
		}
	}
	return
}

func (m *model) renderSwarmBlock(bl block, w int) string {
	c := bl.swarm
	if c == nil {
		return ""
	}
	n := c.Expected
	if n == 0 {
		n = len(c.Members)
	}
	done, failed, cancelled, running, waiting, queued := c.counts()
	detached := ""
	if c.Detached {
		detached = "detached · "
	}
	head := titleStyle.Bold(true).Render("Swarm") + titleStyle.Render(fmt.Sprintf(" %s%d%s", lifeTimes, n, lifeAngle))
	summary := fmt.Sprintf("%s%s %s %d done · %d failed · %d cancelled · %d running · %d waiting · %d queued",
		detached, truncate(c.Desc, max(8, w-80)), emDash, done, failed, cancelled, running, waiting, queued)
	line := head + dimStyle.Render(summary)
	var b strings.Builder
	bar := titleStyle
	if failed > 0 || cancelled > 0 {
		bar = errStyle
	}
	writeAccentWith(&b, bar, line)
	if bl.folded {
		return strings.TrimRight(b.String(), "\n")
	}
	for i, mem := range c.Members {
		title := mem.Item
		if title == "" {
			title = mem.Desc
		}
		if title == "" {
			title = mem.ChildID
		}
		conn := lifeBranch
		if i+1 == len(c.Members) {
			conn = lifeCorner
		}
		detail := ""
		if mem.Turns > 0 {
			detail += fmt.Sprintf(" · %d turns", mem.Turns)
		}
		if mem.Tools > 0 {
			detail += fmt.Sprintf(" · %d tools", mem.Tools)
		}
		if mem.Duration > 0 {
			detail += " · " + formatDuration(mem.Duration)
		}
		if mem.Activity != "" {
			detail += " · " + mem.Activity
		}
		st := mem.Status
		if st == "" {
			st = "queued"
		}
		stStyle := dimStyle.Bold(true)
		switch st {
		case "running":
			stStyle = titleStyle.Bold(true)
		case "waiting":
			stStyle = warnStyle.Bold(true)
		case "completed":
			stStyle = okStyle
		case "failed", "cancelled":
			stStyle = errStyle.Bold(true)
		}
		idx := fmt.Sprintf(" %02d ", mem.Index+1)
		row := titleStyle.Render(conn) + titleStyle.Bold(true).Render(idx) + dimStyle.Render(truncate(title, max(8, w-28))) + "  " + stStyle.Render(st) + dimStyle.Render(detail)
		writeAccentWith(&b, bar, row)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) renderBgTaskBlock(bl block, w int) string {
	c := bl.bgTask
	if c == nil {
		return ""
	}
	display := c.Desc
	if strings.TrimSpace(display) == "" {
		display = strings.ReplaceAll(c.Command, "\n", " ")
	}
	display = truncate(strings.ReplaceAll(display, "\n", " "), max(8, w-24))
	head := dimStyle.Bold(true).Render("Task ")
	verb := "started: "
	switch c.Status {
	case "completed":
		verb = "completed in " + formatDuration(c.Elapsed) + ": "
	case "killed":
		verb = "killed in " + formatDuration(c.Elapsed) + ": "
	case "failed":
		detail := ""
		if c.Signal != "" {
			detail = " (" + c.Signal + ")"
		} else if c.HasExit {
			detail = fmt.Sprintf(" (exit %d)", c.ExitCode)
		}
		verb = "failed in " + formatDuration(c.Elapsed) + ": "
		display += detail
	}
	line := head + dimStyle.Render(verb+display)
	if c.Status == "started" && m.busy {
		var b strings.Builder
		writeAccentWith(&b, userStyle, line)
		return strings.TrimRight(b.String(), "\n")
	}
	if c.Status == "failed" || c.Status == "killed" {
		var b strings.Builder
		writeAccentWith(&b, errStyle, line)
		return strings.TrimRight(b.String(), "\n")
	}
	if c.Status == "completed" {
		var b strings.Builder
		writeAccentWith(&b, okStyle, line)
		return strings.TrimRight(b.String(), "\n")
	}
	return line
}

func (m *model) renderWorkflowCard(bl block, w int) string {
	c := bl.workflow
	if c == nil {
		return ""
	}
	head := dimStyle.Bold(true).Render("Workflow ")
	verb := c.Name + ": "
	switch c.Status {
	case "complete":
		verb = c.Name + " done in " + formatDuration(c.liveElapsed()) + ": "
	case "failed", "interrupted":
		verb = c.Name + " failed in " + formatDuration(c.liveElapsed()) + ": "
	case "cancelled":
		verb = c.Name + " ◌ cancelled after " + formatDuration(c.liveElapsed()) + ": "
	case "user_paused":
		verb = c.Name + " paused at " + formatDuration(c.liveElapsed()) + ": "
	}
	obj := strings.ReplaceAll(c.Objective, "\n", " ")
	trail := workflowPhaseTrail(c.Phases)
	extra := ""
	if trail != "" {
		extra += "  [" + trail + "]"
	}
	if c.isActive() {
		n := 0
		for _, a := range c.Agents {
			if a.State == "running" {
				n++
			}
		}
		if n > 0 {
			extra += fmt.Sprintf("  (%d agents)", n)
		}
	}
	style := dimStyle
	if c.Status == "cancelled" {
		style = dimStyle
	}
	line := head + style.Render(truncate(verb+obj, max(8, w-12))+extra)
	if c.isActive() {
		var b strings.Builder
		writeAccentWith(&b, userStyle, line)
		return strings.TrimRight(b.String(), "\n")
	}
	return line
}

func workflowPhaseTrail(phases []workflowPhase) string {
	if len(phases) == 0 {
		return ""
	}
	var parts []string
	for _, p := range phases {
		mark := "○"
		switch p.Status {
		case "done":
			mark = "✓"
		case "running", "active":
			mark = "●"
		}
		parts = append(parts, p.Title+" "+mark)
	}
	return strings.Join(parts, " · ")
}

func parseTaskCard(argsBody string) *subagentCard {
	args := parseToolArgs(argsBody)
	if args == nil {
		args = map[string]string{}
	}
	desc := toolArgValue(args, "description", "prompt")
	if desc == "" {
		desc = strings.TrimSpace(argsBody)
	}
	return &subagentCard{
		Desc:       desc,
		SubType:    toolArgValue(args, "subagent_type", "type"),
		Persona:    toolArgValue(args, "persona"),
		Role:       toolArgValue(args, "role"),
		Model:      toolArgValue(args, "model"),
		Background: args["background"] == "true" || args["run_in_background"] == "true",
		Status:     "running",
	}
}

func parseBgTaskCard(argsBody string) *bgTaskCard {
	args := parseToolArgs(argsBody)
	prompt := toolArgValue(args, "prompt", "command", "description")
	if prompt == "" {
		prompt = strings.TrimSpace(argsBody)
	}
	id := toolArgValue(args, "task_id", "id")
	return &bgTaskCard{
		Command: prompt,
		TaskID:  id,
		Desc:    prompt,
		Status:  "started",
	}
}

func (m *model) finishSubagent(result string, failed bool) {
	elapsed := time.Duration(0)
	if !m.turnStarted.IsZero() {
		elapsed = time.Since(m.turnStarted)
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind != blockSubagent || m.blocks[i].subagent == nil {
			continue
		}
		c := m.blocks[i].subagent
		if c.Status != "running" {
			continue
		}
		c.Elapsed = elapsed
		switch {
		case failed && (strings.Contains(strings.ToLower(result), "cancel") || strings.Contains(strings.ToLower(result), "canceled")):
			c.Status = "cancelled"
		case failed:
			c.Status = "failed"
			c.Err = truncate(result, 80)
		default:
			c.Status = "completed"
		}
		if c.ChildID == "" {
			c.ChildID = extractChildID(result)
		}
		m.finishSwarmMember(c)
		return
	}
}

func extractChildID(s string) string {
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "sess-") || strings.HasPrefix(tok, "subagent") {
			return strings.Trim(tok, ":,")
		}
	}
	return ""
}

func (m *model) finishBgTask(result string, failed bool) {
	elapsed := time.Duration(0)
	if !m.turnStarted.IsZero() {
		elapsed = time.Since(m.turnStarted)
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind != blockBgTask || m.blocks[i].bgTask == nil {
			continue
		}
		c := m.blocks[i].bgTask
		if c.Status != "started" {
			continue
		}
		c.Elapsed = elapsed
		if id := toolArgValue(parseToolArgs(result), "id", "task_id"); id != "" {
			c.TaskID = id
		} else if strings.Contains(result, "id") {
			c.TaskID = truncate(result, 24)
		}
		low := strings.ToLower(result)
		switch {
		case strings.Contains(low, "sigkill") || strings.Contains(low, "killed"):
			c.Status = "killed"
			c.Signal = "killed"
		case failed:
			c.Status = "failed"
		default:
			c.Status = "completed"
		}
		return
	}
}

func (m *model) lastSwarm() *swarmCard {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockSwarm && m.blocks[i].swarm != nil {
			return m.blocks[i].swarm
		}
	}
	return nil
}

func (m *model) noteSwarmMember(card *subagentCard) {
	if card == nil || (!m.swarm && m.lastSwarm() == nil) {
		return
	}
	sw := m.lastSwarm()
	if sw == nil {
		sw = &swarmCard{Desc: card.Desc, Expected: 0}
		m.appendBlock(block{kind: blockSwarm, swarm: sw, folded: true, at: time.Now()})
	}
	sw.Members = append(sw.Members, swarmMember{
		Index:   len(sw.Members),
		Desc:    card.Desc,
		ChildID: card.ChildID,
		Status:  "running",
	})
	if sw.Expected < len(sw.Members) {
		sw.Expected = len(sw.Members)
	}
}

func (m *model) finishSwarmMember(card *subagentCard) {
	sw := m.lastSwarm()
	if sw == nil || card == nil {
		return
	}
	for i := range sw.Members {
		mem := &sw.Members[i]
		if mem.Status != "running" {
			continue
		}
		if card.ChildID != "" && mem.ChildID != "" && mem.ChildID != card.ChildID {
			continue
		}
		if mem.Desc != "" && card.Desc != "" && mem.Desc != card.Desc && mem.ChildID == "" {
			continue
		}
		mem.Status = card.Status
		mem.Duration = card.Elapsed
		if mem.ChildID == "" {
			mem.ChildID = card.ChildID
		}
		return
	}
}

func (m *model) syncWorkflowBlock(run workflowRun) {
	cp := run
	for i := range m.blocks {
		if m.blocks[i].kind == blockWorkflow && m.blocks[i].workflow != nil && m.blocks[i].workflow.ID == run.ID {
			m.blocks[i].workflow = &cp
			return
		}
	}
	m.appendBlock(block{kind: blockWorkflow, workflow: &cp, at: time.Now()})
}
