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
	blockGoal
	blockMermaid
	blockImage
	blockBtw
)

type block struct {
	kind   blockKind
	title  string
	body   string
	err    bool
	at     time.Time
	folded bool
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
		if i > 0 && b.Len() > 0 {
			b.WriteByte('\n')
		}
		if m.groupTools {
			if run, ok := m.runStartingAt(i); ok {
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
		body := m.renderBlock(bl, w)
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
			if n >= 1 {
				return r, true
			}
		}
	}
	return verbRun{}, false
}

func (m *model) renderBlock(bl block, w int) string {
	if bl.folded {
		return dimStyle.Render("▸ " + truncate(bl.body, 60))
	}
	stamp := ""
	if m.timestamps && !bl.at.IsZero() {
		stamp = dimStyle.Render(" " + bl.at.Format("15:04:05"))
	}
	switch bl.kind {
	case blockUser:
		return userLabel.Render("YOU") + stamp + "\n" + renderMarkdownEx(bl.body, w, m.mermaidOn())
	case blockAgent:
		return agentLabel.Render("ENTROPY") + stamp + "\n" + renderMarkdownEx(bl.body, w, m.mermaidOn())
	case blockThinking:
		return dimStyle.Render("thinking") + "\n" + dimStyle.Render(bl.body)
	case blockTool:
		inner := toolStyle.Render(bl.title) + "\n" + dimStyle.Render(truncate(bl.body, 600))
		return toolCard.Width(w - 2).Render(inner)
	case blockToolResult:
		body := truncate(bl.body, 800)
		st := dimStyle
		card := toolCard
		if bl.err {
			st = errStyle
			card = errCard
		}
		if strings.EqualFold(bl.title, "update_goal") {
			card = goalCard
		}
		inner := dimStyle.Render(bl.title) + "\n" + st.Render(body)
		return card.Width(w - 2).Render(inner)
	case blockGoal:
		inner := goalTitle.Render("GOAL") + "\n" + bl.body
		return goalCard.Width(w - 2).Render(inner)
	case blockMermaid:
		if m.mermaidOn() {
			return renderMermaidASCII(bl.body, w)
		}
		return mermaidFallbackCard(bl.body, w)
	case blockBtw:
		return m.renderBtwBlock(bl, w)
	case blockImage:
		inner := toolStyle.Render("image") + "\n" + dimStyle.Render(truncate(bl.body, 400))
		return toolCard.Width(w - 2).Render(inner)
	default:
		return sysLabel.Render(bl.body)
	}
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
		body := "▎ " + text + cursor + dimStyle.Render(ghost)
		return inputBox.Width(w).Render(body)
	}
	return inputBox.Width(w).Render(m.ta.View())
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
	if panel := m.renderBtwPanel(); panel != "" {
		input = panel + "\n" + input
	}
	if banner := m.renderVoiceBanner(); banner != "" {
		input = banner + "\n" + input
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
		lines += strings.Count(m.renderBlock(m.blocks[i], w), "\n") + 2
	}
	return lines
}

func (m *model) appendBlock(bl block) {
	m.blocks = append(m.blocks, bl)
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
