package pager

import (
	"fmt"
	"strings"

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
)

type block struct {
	kind  blockKind
	title string
	body  string
	err   bool
}

func (m *model) transcriptWidth() int {
	w := m.width - 4
	if m.showSidebar() {
		w -= m.sidebarWidth() + 1
	}
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
	return m.width >= 92
}

func (m *model) renderTranscript() string {
	w := m.transcriptWidth()
	var b strings.Builder
	for i, bl := range m.blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.renderBlock(bl, w))
	}
	return b.String()
}

func (m *model) renderBlock(bl block, w int) string {
	switch bl.kind {
	case blockUser:
		return userLabel.Render("YOU") + "\n" + renderMarkdown(bl.body, w)
	case blockAgent:
		return agentLabel.Render("OPEN GROK") + "\n" + renderMarkdown(bl.body, w)
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
	default:
		return sysLabel.Render(bl.body)
	}
}

func (m *model) renderHeader() string {
	left := titleStyle.Render(" Open Grok ") + dimStyle.Render(" "+version.String())
	mid := fmt.Sprintf(" %s  ·  %s", m.opts.Model.Name, m.opts.Model.Provider.DisplayName())
	status := m.status
	if m.busy {
		status = m.spin.View() + " " + m.status
	}
	right := " " + status + " "
	plan := "plan off"
	if m.sess.Plan.Active {
		plan = "plan ON"
	}
	yolo := "ask"
	if m.sess.YOLO {
		yolo = "yolo"
	}
	goal := m.goalBadge()
	meta := dimStyle.Render(fmt.Sprintf("  session %s  ·  %s  ·  %s  ·  %s", shortID(m.sess.ID), yolo, plan, goal))
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, dimStyle.Render(mid), lipgloss.NewStyle().Width(max(0, m.width-lipgloss.Width(left)-lipgloss.Width(mid)-lipgloss.Width(right))).Render(""), dimStyle.Render(right))
	if m.width > 0 {
		top = lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Render(top)
	}
	return top + "\n" + meta
}

func (m *model) goalBadge() string {
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
	snap := m.sess.Goal.Snapshot()
	var b strings.Builder
	b.WriteString(goalTitle.Render("GOALS") + "\n")
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
	hint := "enter send  ·  ctrl+c quit  ·  /goal  ·  /help"
	val := strings.TrimSpace(m.ta.Value())
	if strings.HasPrefix(val, "/") {
		matches := matchingSlash(val)
		if len(matches) > 0 {
			var parts []string
			for i, c := range matches {
				if i >= 6 {
					break
				}
				parts = append(parts, "/"+c.Name)
			}
			hint = strings.Join(parts, "  ")
		}
	}
	return dimStyle.Render(hint)
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
