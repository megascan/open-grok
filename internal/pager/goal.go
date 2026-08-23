package pager

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) openGoalDetail() {
	if m.sess == nil || m.sess.Goal == nil || m.sess.Goal.Snapshot() == nil {
		m.appendSys("no active goal — /goal <objective>")
		return
	}
	m.overlay = overlayGoal
}

func (m *model) handleGoalOverlayKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "g":
		m.overlay = overlayNone
	}
	return nil
}

func (m *model) renderGoalDetail(w int) string {
	if m.sess == nil || m.sess.Goal == nil {
		return modalBox.Width(w).Render(titleStyle.Render("Goal") + "\n\n" + dimStyle.Render("no session"))
	}
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		return modalBox.Width(w).Render(titleStyle.Render("Goal") + "\n\n" + dimStyle.Render("No goal. /goal <objective> [--budget <tokens>]"))
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Goal") + "  " + m.goalBadge() + "\n")
	fmt.Fprintf(&b, "%s  ·  %s  ·  %s\n\n", snap.Status, snap.Phase, formatElapsed(uint64(snap.ElapsedMS)))
	b.WriteString(renderMarkdown(snap.Objective, w-6) + "\n")
	if snap.TokenBudget != nil && *snap.TokenBudget > 0 {
		used := 0
		if m.sess != nil {
			for _, msg := range m.sess.Messages {
				used += len(msg.Content) / 4
			}
		}
		pct := float64(used) / float64(*snap.TokenBudget)
		if pct > 1 {
			pct = 1
		}
		fill := int(pct * 24)
		bar := strings.Repeat("█", fill) + strings.Repeat("░", 24-fill)
		fmt.Fprintf(&b, "\nbudget  %s  %s / %s\n", bar, fmtTokens(used), fmtTokens(int(*snap.TokenBudget)))
	}
	if snap.PauseMessage != "" {
		b.WriteString("\n" + errStyle.Render(snap.PauseMessage) + "\n")
	}
	if snap.CompletionClaimed {
		b.WriteString("\n" + claimStyle.Render("claim (run /goal complete to verify):") + "\n")
		b.WriteString(dimStyle.Render(truncate(snap.CompletionSummary, 400)) + "\n")
	}
	if n := len(snap.Progress); n > 0 {
		b.WriteString("\n" + dimStyle.Render("progress") + "\n")
		start := 0
		if n > 15 {
			start = n - 15
		}
		for _, p := range snap.Progress[start:] {
			b.WriteString(bulletMd.Render("• ") + p + "\n")
		}
	}
	if n := len(snap.History); n > 0 {
		b.WriteString("\n" + dimStyle.Render("history") + "\n")
		start := 0
		if n > 12 {
			start = n - 12
		}
		for _, h := range snap.History[start:] {
			line := h.Event
			if h.Detail != "" {
				line += " · " + truncate(h.Detail, 40)
			}
			if !h.At.IsZero() {
				line += "  " + h.At.Format(time.Kitchen)
			}
			b.WriteString(dimStyle.Render("· "+line) + "\n")
		}
	}
	b.WriteString("\n" + dimStyle.Render("esc / g close  ·  /goal pause resume complete clear"))
	return modalBox.Width(w).Render(b.String())
}

func formatElapsed(ms uint64) string {
	secs := ms / 1000
	h, mins, s := secs/3600, (secs%3600)/60, secs%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm%02ds", mins, s)
	}
	return fmt.Sprintf("%ds", s)
}
