package pager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/goals"
	"github.com/megascan/open-grok/internal/version"
)

type blockKind int

const (
	blockUser blockKind = iota
	blockAgent
	blockThinking
	blockTool
	blockToolResult
	blockSystem
	blockSession
	blockSubagent
	blockSwarm
	blockBgTask
	blockWorkflow
	blockGoal
	blockMermaid
	blockImage
	blockBtw
)

type block struct {
	kind     blockKind
	title    string
	body     string
	err      bool
	at       time.Time
	folded   bool
	event    sessionEvent
	hooks    toolHookData
	subagent *subagentCard
	swarm    *swarmCard
	bgTask   *bgTaskCard
	workflow *workflowRun
}

func (m *model) transcriptWidth() int {
	w := m.width - 4
	if m.showSidebar() {
		w -= m.sidebarWidth() + 1
	}
	w -= m.timelineRailWidth()
	if w < 24 {
		return 24
	}
	return w
}

func (m *model) sidebarWidth() int {
	if m.width >= 110 {
		return 34
	}
	return 28
}

func (m *model) showSidebar() bool {
	return m.screenMd != modeMinimal && m.width >= 92
}

func (m *model) renderTranscript() string {
	w := m.transcriptWidth()
	var b strings.Builder
	skipUntil := -1
	for i, bl := range m.blocks {
		if i < skipUntil {
			continue
		}
		if i > 0 && bl.kind == blockToolResult && m.blocks[i-1].kind == blockTool {
			continue
		}
		if bl.kind == blockAgent && strings.TrimSpace(bl.body) == "" {
			continue
		}
		if i > 0 && b.Len() > 0 {
			b.WriteByte('\n')
		}
		if m.groupTools {
			if run, ok := m.runStartingAt(i); ok && m.toolCount(run) >= 2 {
				header := m.renderVerbHeader(run, w)
				if m.focusScroll && i == m.selectedBlock {
					header = selStyle.Render("▌") + " " + header
				}
				b.WriteString(header)
				if !m.groupExpanded(run) {
					skipUntil = run.End
					continue
				}
				b.WriteByte('\n')
			}
		}
		body := m.renderBlockAt(i, w)
		if body == "" {
			continue
		}
		if m.focusScroll && i == m.selectedBlock {
			body = selStyle.Render("▌") + " " + body
		}
		b.WriteString(body)
	}
	return b.String()
}

func (m *model) runStartingAt(i int) (verbRun, bool) {
	for _, r := range m.verbRuns() {
		if r.Start == i {
			n := 0
			for k := r.Start; k < r.End; k++ {
				if m.blocks[k].kind == blockTool {
					n++
				}
			}
			if n >= 2 {
				return r, true
			}
		}
	}
	return verbRun{}, false
}

func (m *model) renderBlock(bl block, w int) string {
	for i, existing := range m.blocks {
		if existing.kind == bl.kind && existing.title == bl.title && existing.body == bl.body && existing.at.Equal(bl.at) {
			return m.renderBlockAt(i, w)
		}
	}
	saved := m.blocks
	m.blocks = []block{bl}
	out := m.renderBlockAt(0, w)
	m.blocks = saved
	return out
}

func (m *model) renderBlockAt(idx int, w int) string {
	if idx < 0 || idx >= len(m.blocks) {
		return ""
	}
	bl := m.blocks[idx]
	if bl.folded && !lifecycleChrome(bl.kind) && bl.kind != blockUser && bl.kind != blockThinking && bl.kind != blockTool && bl.kind != blockSession {
		return dimStyle.Render("▸ " + truncate(bl.body, 60))
	}
	stamp := ""
	if m.timestamps && !bl.at.IsZero() {
		stamp = dimStyle.Render(" " + bl.at.Format("15:04:05"))
	}
	switch bl.kind {
	case blockUser:
		return m.renderUserBlock(bl, w) + stamp
	case blockAgent:
		if strings.TrimSpace(bl.body) == "" {
			return ""
		}
		return renderMarkdownEx(bl.body, w, m.mermaidOn())
	case blockThinking:
		return m.renderThinkingBlock(bl, idx, w)
	case blockTool:
		return m.renderToolBlock(idx, w)
	case blockToolResult:
		return renderAccentBlock(strings.TrimLeft(bl.title, "▸ "), truncateResultBody(bl.body), bl.err)
	case blockSession:
		return m.renderSessionEvent(bl, w)
	case blockSubagent:
		return m.renderSubagentBlock(bl, w)
	case blockSwarm:
		return m.renderSwarmBlock(bl, w)
	case blockBgTask:
		return m.renderBgTaskBlock(bl, w)
	case blockWorkflow:
		return m.renderWorkflowCard(bl, w)
	case blockGoal:
		inner := goalTitle.Render("GOAL") + "\n" + bl.body
		return goalCard.Width(w - 2).Render(inner)
	case blockMermaid:
		if m.mermaidOn() {
			return renderMermaidASCIIEx(bl.body, w, m.mermaidPendingFor(bl.body))
		}
		return mermaidFallbackCard(bl.body, w)
	case blockBtw:
		return m.renderBtwBlock(bl, w)
	case blockImage:
		return renderAccentBlock("image", truncate(bl.body, 400), false)
	default:
		return sysLabel.Render(bl.body)
	}
}

func (m *model) renderUserBlock(bl block, w int) string {
	text := bl.body
	if bl.folded {
		text, _ = collapseUserLines(text)
	}
	prefix := userStyle.Render(userPromptPrefix(bl.body))
	body := renderMarkdownEx(text, max(8, w-3), m.mermaidOn())
	lines := strings.Split(body, "\n")
	var b strings.Builder
	hang := strings.Repeat(" ", 2)
	for i, line := range lines {
		if i == 0 {
			b.WriteString(prefix + line)
		} else {
			b.WriteString("\n" + hang + line)
		}
	}
	return b.String()
}

func (m *model) renderThinkingBlock(bl block, idx, w int) string {
	running := m.busy && m.lastThinkingIndex() == idx
	header := "Thought"
	if running {
		header = "Thinking…"
	}
	var b strings.Builder
	writeAccent(&b, dimStyle.Bold(true).Render(header))
	if bl.folded && !running {
		return strings.TrimRight(b.String(), "\n")
	}
	body := strings.TrimSpace(bl.body)
	if body == "" {
		return strings.TrimRight(b.String(), "\n")
	}
	shown := body
	if !running {
		lines := strings.Split(body, "\n")
		if len(lines) > 8 {
			shown = "…\n" + strings.Join(lines[len(lines)-6:], "\n")
		}
	}
	for _, line := range strings.Split(shown, "\n") {
		writeAccent(&b, dimStyle.Italic(true).Render(line))
	}
	_ = w
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) lastThinkingIndex() int {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockThinking {
			return i
		}
	}
	return -1
}

func (m *model) renderToolBlock(idx, w int) string {
	if idx < 0 || idx >= len(m.blocks) {
		return ""
	}
	bl := m.blocks[idx]
	result := ""
	failed := bl.err
	if idx+1 < len(m.blocks) && m.blocks[idx+1].kind == blockToolResult {
		rb := m.blocks[idx+1]
		result = rb.body
		failed = failed || rb.err
	} else if strings.TrimSpace(bl.body) != "" && parseToolArgs(bl.body) == nil {
		result = bl.body
	}
	_ = w
	return renderToolCard(bl.title, bl.body, result, failed, bl.folded, bl.hooks)
}

func (m *model) mermaidOn() bool {
	return m.cfg.PrefBool("render_mermaid", m.cfg.UI.RenderMermaid, true)
}

func (m *model) renderPromptInput() string {
	w := max(20, m.width-2)
	text := m.ta.Value()
	ghost := m.suggest.ghostFor(text)
	if ghost != "" && !strings.Contains(text, "\n") && !m.focusScroll {
		cursor := "█"
		body := m.promptPrefix() + text + cursor + dimStyle.Render(ghost)
		return inputBox.Width(w).Render(body)
	}
	return inputBox.Width(w).Render(m.ta.View())
}

func (m *model) promptPrefix() string {
	if m.overlay == overlayHistory {
		return historySearchPrefix
	}
	if m.rememberMode {
		return rememberPrefix
	}
	return promptArrow
}

func fmtTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dK", n/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%dM", n/1_000_000)
	}
}

func (m *model) renderHeader() string {
	left := titleStyle.Render(" Entropy ") + dimStyle.Render(" "+version.String())
	mid := fmt.Sprintf(" %s  ·  %s", m.opts.Model.Name, m.opts.Model.Provider.DisplayName())
	status := m.status
	if m.busy && !m.turnStatusVisible() {
		status = m.spin.View() + " " + m.status
	}
	right := " " + status + " "
	plan := "plan off"
	if m.sess != nil && m.sess.Plan.Active {
		plan = "plan ON"
	}
	yolo := "ask"
	switch m.permMode {
	case permAlways:
		yolo = "yolo"
	case permAuto:
		yolo = "auto"
	case permPlan:
		yolo = "plan"
	}
	sid := ""
	ctx := "0 / 0"
	if m.sess != nil {
		sid = shortID(m.sess.ID)
		n := 0
		for _, msg := range m.sess.Messages {
			n += len(msg.Content)
		}
		tok := n / 4
		win := m.opts.Model.ContextWindow
		if win == 0 {
			win = 128_000
		}
		ctx = contextBarText(tok, win, m.ctxHover)
	}
	goal := m.goalBadge()
	meta := dimStyle.Render("  session "+sid+"  ") + dimStyle.Render(contextBarSep+"  ") +
		ctx + dimStyle.Render("  "+contextBarSep+"  ") +
		dimStyle.Render(joinSep(yolo, plan, goal))
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, dimStyle.Render(mid), lipgloss.NewStyle().Width(max(0, m.width-lipgloss.Width(left)-lipgloss.Width(mid)-lipgloss.Width(right))).Render(""), dimStyle.Render(right))
	if m.width > 0 {
		top = lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Render(top)
	}
	return top + "\n" + meta
}

func (m *model) goalBadge() string {
	if m.sess == nil || m.sess.Goal == nil {
		return dimStyle.Render("goal off")
	}
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		return dimStyle.Render("goal off")
	}
	switch {
	case snap.CompletionClaimed && snap.Status == goals.StatusActive:
		return claimStyle.Render("goal claimed")
	case snap.Status == goals.StatusActive:
		return okStyle.Render("goal " + string(snap.Status))
	case snap.Status == goals.StatusComplete:
		return okStyle.Render("goal complete")
	case snap.Status.IsPaused():
		return errStyle.Render("goal " + string(snap.Status))
	default:
		return dimStyle.Render("goal " + string(snap.Status))
	}
}

func (m *model) renderSidebar() string {
	w := m.sidebarWidth() - 2
	var b strings.Builder
	if todo := m.renderTodoPane(w); todo != "" {
		b.WriteString(todo)
		b.WriteString("\n")
	}
	b.WriteString(goalTitle.Render("GOALS") + "\n")
	if m.sess == nil || m.sess.Goal == nil {
		b.WriteString(dimStyle.Render("No session."))
		return panelStyle.Width(m.sidebarWidth() - 2).Height(m.vp.Height).Render(b.String())
	}
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		b.WriteString(dimStyle.Render("No goal running.") + "\n\n")
		b.WriteString(dimStyle.Render("/goal <objective>") + "\n")
		b.WriteString(dimStyle.Render("/goal status|pause") + "\n")
		b.WriteString(dimStyle.Render("/goal resume|clear") + "\n")
		b.WriteString(dimStyle.Render("/goal complete") + "\n")
		return panelStyle.Width(m.sidebarWidth() - 2).Height(m.vp.Height).Render(b.String())
	}
	st := string(snap.Status)
	if snap.CompletionClaimed && snap.Status == goals.StatusActive {
		st = "claimed"
	}
	b.WriteString(m.goalBadge() + "\n")
	b.WriteString(dimStyle.Render(st+" · "+string(snap.Phase)) + "\n\n")
	b.WriteString(renderMarkdown(snap.Objective, w) + "\n")
	if snap.PauseMessage != "" {
		b.WriteString("\n" + errStyle.Render(snap.PauseMessage) + "\n")
	}
	if snap.CompletionSummary != "" {
		b.WriteString("\n" + claimStyle.Render("claim:") + "\n")
		b.WriteString(dimStyle.Render(truncate(snap.CompletionSummary, 240)) + "\n")
	}
	if n := len(snap.Progress); n > 0 {
		b.WriteString("\n" + dimStyle.Render("progress") + "\n")
		start := 0
		if n > 6 {
			start = n - 6
		}
		for _, p := range snap.Progress[start:] {
			b.WriteString(bulletMd.Render("• ") + dimStyle.Render(truncate(p, w-2)) + "\n")
		}
	}
	if n := len(snap.History); n > 0 {
		b.WriteString("\n" + dimStyle.Render("history") + "\n")
		start := 0
		if n > 8 {
			start = n - 8
		}
		for _, h := range snap.History[start:] {
			line := h.Event
			if h.Detail != "" {
				line += " · " + truncate(h.Detail, 18)
			}
			b.WriteString(dimStyle.Render("· "+line) + "\n")
		}
	}
	b.WriteString("\n" + dimStyle.Render("/goal pause resume") + "\n")
	b.WriteString(dimStyle.Render("/goal complete clear"))
	return panelStyle.Width(m.sidebarWidth() - 2).Height(m.vp.Height).Render(b.String())
}

func (m *model) renderFooter() string {
	hint := "enter send  ·  shift+tab mode  ·  ctrl+\\ dashboard  ·  / commands"
	if m.multiline {
		hint = "alt+enter send  ·  enter newline  ·  shift+tab mode  ·  / commands"
	}
	if m.focusScroll {
		hint = "tab prompt  ·  j/k nav  ·  y copy  ·  e expand"
	}
	if g := m.suggest.ghostFor(m.ta.Value()); g != "" && !m.focusScroll {
		hint = "tab accept  ·  ctrl+right one word  ·  esc dismiss"
	}
	val := strings.TrimSpace(m.ta.Value())
	if strings.HasPrefix(val, "/") {
		if m.overlay == overlayPalette {
			hint = "tab complete  ·  enter run  ·  esc"
		} else {
			matches := FuzzySlash(val, m.screenMd)
			if len(matches) > 0 {
				var parts []string
				for i, c := range matches {
					if i >= 8 {
						break
					}
					parts = append(parts, "/"+c.Name)
				}
				hint = strings.Join(parts, "  ")
			}
		}
	}
	if m.copied != "" {
		hint = okStyle.Render(m.status)
		if !strings.HasPrefix(m.status, "Cop") && m.copyToast != "" {
			hint = okStyle.Render(m.copyToast)
		}
	}
	if m.vimMode && m.focusScroll {
		hint = "i/Tab/Space prompt  ·  j/k blocks  ·  y copy  ·  e expand group"
	}
	return dimStyle.Render(hint)
}

func (m *model) viewChat() string {
	head := m.renderHeader()
	if m.dashDetails {
		head = m.renderDashDetailsHeader() + "\n" + head
	}
	privacy := ""
	if m.showPrivacyBanner() {
		privacy = privacyBanner()
		if !strings.HasSuffix(privacy, "\n") {
			privacy += "\n"
		}
	}
	announce := ""
	if m.showAnnouncementBanner() {
		announce = announcementBanner()
		if !strings.HasSuffix(announce, "\n") {
			announce += "\n"
		}
	}
	hud := ""
	if m.scrollHUD {
		hud = dimStyle.Render(fmt.Sprintf("scroll y=%d/%d blocks=%d", m.vp.YOffset, m.vp.TotalLineCount(), len(m.blocks))) + "\n"
	}
	prefix := head + "\n" + barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n"
	m.privacyY = -1
	if privacy != "" {
		m.privacyY = strings.Count(prefix, "\n")
		prefix += privacy
	}
	prefix += announce
	prefix += hud
	m.transcriptTop = strings.Count(prefix, "\n")

	vpView := m.vp.View()
	if rail := m.renderTimelineRail(); rail != "" {
		vpView = lipgloss.JoinHorizontal(lipgloss.Top, rail, vpView)
	}
	body := vpView
	if m.showSidebar() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, vpView, m.renderSidebar())
	}
	turn := m.renderTurnStatus()
	if turn != "" {
		m.turnStatusY = m.transcriptTop + m.vp.Height
		turn += "\n"
	} else {
		m.turnStatusY = -1
	}
	chips := m.renderChips()
	input := m.renderPromptInput()
	if dd := m.renderSlashDropdown(); dd != "" {
		input = dd + "\n" + input
	} else if dd := m.renderFileDropdown(); dd != "" {
		input = dd + "\n" + input
	} else if dd := m.renderHistoryDropdown(); dd != "" {
		input = dd + "\n" + input
	}
	if panel := m.renderBtwPanel(); panel != "" {
		input = panel + "\n" + input
	}
	if banner := m.renderVoiceBanner(); banner != "" {
		input = banner + "\n" + input
	}
	if m.modeBanner != "" {
		input = titleStyle.Render(m.modeBanner) + "\n" + input
	}
	return prefix + body + "\n" + turn + chips + input + "\n" + m.renderFooter()
}

func (m *model) renderDashDetailsHeader() string {
	title := "(untitled)"
	if m.sess != nil && m.sess.Title != "" {
		title = m.sess.Title
	}
	right := "[Dashboard]"
	ids := m.attachableDashIDs()
	if n := len(ids); n > 1 && m.sess != nil {
		for i, id := range ids {
			if id == m.sess.ID {
				// Rust paints `{i}/{n} [‹][›] [Dashboard]` with flush cycle chips.
				right = fmt.Sprintf("%d/%d [‹][›] [Dashboard]", i+1, n)
				break
			}
		}
	}
	left := " " + title
	inner := max(20, m.width)
	gap := inner - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return headerBar.Width(inner).Render(left + strings.Repeat(" ", gap) + right)
}

func (m *model) scrollToBlock(idx int) {
	m.jumpToBlock(idx)
}

// jumpToBlock scrolls the transcript so block idx is at the viewport top.
// Unlike send-time page-flip, this is used by /jump live preview and the
// timeline rail and is not gated on page_flip_on_send.
func (m *model) jumpToBlock(idx int) {
	m.vp.SetContent(m.renderTranscript())
	if idx <= 0 {
		m.vp.GotoTop()
		return
	}
	m.vp.SetYOffset(m.blockLineOffset(idx))
}

func (m *model) blockLineOffset(idx int) int {
	lines := 0
	w := m.transcriptWidth()
	for i := 0; i < idx && i < len(m.blocks); i++ {
		lines += strings.Count(m.renderBlockAt(i, w), "\n") + 2
	}
	return lines
}

func lifecycleChrome(k blockKind) bool {
	switch k {
	case blockSubagent, blockSwarm, blockBgTask, blockWorkflow:
		return true
	default:
		return false
	}
}

func (m *model) appendBlock(bl block) {
	if bl.kind == blockUser {
		nlines := strings.Count(bl.body, "\n") + 1
		if nlines > userCollapsedMaxLines || len(bl.body) > userCollapsedMaxLines*60 {
			bl.folded = true
		}
	}
	if bl.kind == blockTool {
		if classifyTool(bl.title) == verbEdit {
			bl.folded = m.collapsedEditBlocks()
		} else {
			bl.folded = true
		}
	}
	m.blocks = append(m.blocks, bl)
}

func (m *model) collapsedEditBlocks() bool {
	return m.cfg.PrefBool("collapsed_edit_blocks", m.cfg.UI.CollapsedEdits, false)
}

func (m *model) lastBlock(kind blockKind) *block {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == kind {
		return &m.blocks[n-1]
	}
	return nil
}

func (m *model) refreshVP() {
	m.vp.SetContent(m.renderTranscript())
	m.vp.GotoBottom()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
