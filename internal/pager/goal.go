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
		var b strings.Builder
		writeAccent(&b, titleStyle.Render("Goal"))
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("no session"))
		return modalBox.Width(w).Render(b.String())
	}
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		var b strings.Builder
		writeAccent(&b, titleStyle.Render("Goal"))
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("No goal. /goal <objective> [--budget <tokens>]"))
		return modalBox.Width(w).Render(b.String())
	}
	var b strings.Builder
	writeAccent(&b, titleStyle.Render("Goal")+"  "+m.goalBadge())
	writeAccent(&b, fmt.Sprintf("%s  ·  %s  ·  %s", snap.Status, snap.Phase, formatElapsed(uint64(snap.ElapsedMS))))
	writeAccent(&b, "")
	writeAccentLines(&b, strings.TrimRight(renderMarkdown(snap.Objective, w-6), "\n"))
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
		writeAccent(&b, "")
		writeAccent(&b, fmt.Sprintf("budget  %s  %s / %s", bar, fmtTokens(used), fmtTokens(int(*snap.TokenBudget))))
	}
	if snap.PauseMessage != "" {
		writeAccent(&b, "")
		writeAccent(&b, errStyle.Render(snap.PauseMessage))
	}
	if snap.CompletionClaimed {
		writeAccent(&b, "")
		writeAccent(&b, claimStyle.Render("claim (run /goal complete to verify):"))
		writeAccent(&b, dimStyle.Render(truncate(snap.CompletionSummary, 400)))
	}
	if n := len(snap.Progress); n > 0 {
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("progress"))
		start := 0
		if n > 15 {
			start = n - 15
		}
		for _, p := range snap.Progress[start:] {
			writeAccent(&b, bulletMd.Render("• ")+p)
		}
	}
	if n := len(snap.History); n > 0 {
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("history"))
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
			writeAccent(&b, dimStyle.Render("· "+line))
		}
	}
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("esc / g close  ·  /goal pause resume complete clear"))
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
