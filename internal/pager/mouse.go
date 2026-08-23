package pager

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// handleMouse hit-tests last-frame regions: overlays ignore clicks, then
// prompt vs scrollback vs dashboard rows. Matches Rust pane order enough for
// click-to-focus and click-to-select-block.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.overlay == overlayGboom {
		if tea.MouseEvent(msg).IsWheel() {
			return nil, true
		}
		return m.handleGboomMouse(msg), true
	}
	if tea.MouseEvent(msg).IsWheel() {
		return m.handleMouseWheel(msg)
	}
	if msg.Action == tea.MouseActionMotion {
		hovered := m.screen == screenChat && msg.Y <= 1
		if m.dashDetails {
			hovered = m.screen == screenChat && msg.Y <= 2
		}
		if m.ctxHover != hovered {
			m.ctxHover = hovered
		}
		if m.screen == screenChat {
			stop := m.stopBtnX >= 0 && msg.Y == m.turnStatusY &&
				msg.X >= m.stopBtnX && msg.X < m.stopBtnX+m.stopBtnW
			m.stopHover = stop
			if m.lastRail != nil {
				m.railHover = m.lastRail.hit(msg.X, msg.Y)
			} else {
				m.railHover = timelineHitNone
			}
		}
		return nil, true
	}
	if m.overlay != overlayNone && !m.isPromptOverlay() {
		return nil, true
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil, false
	}
	y := msg.Y
	switch m.screen {
	case screenDashboard:
		off := 1
		if m.dashSearch {
			off++
		}
		idx := y - off
		if idx >= 0 && idx < len(m.dashRows) {
			m.cursor = idx
			m.peekReply = ""
			m.peekQSel = -1
			m.fillDashPeek()
			row := m.dashRows[idx]
			if row.State == "header" && strings.HasPrefix(row.Header, "Inactive") {
				m.inactiveCollapsed = !m.inactiveCollapsed
				m.refreshDashboard()
				return nil, true
			}
			if row.State == "more" {
				m.dashIdleExpand = true
				m.refreshDashboard()
				return nil, true
			}
			if row.State == "new" {
				return m.newDashSession(), true
			}
			if row.Sess.ID != "" {
				return m.attachDashRow(), true
			}
		}
		return nil, true
	case screenWelcome, screenLogin:
		return nil, false
	}
	if m.screen != screenChat {
		return nil, false
	}
	if m.showPrivacyBanner() {
		if hit := m.hitPrivacy(msg.X, y); hit == "in" {
			m.ackPrivacyBanner(true)
			m.layoutChat()
			return nil, true
		} else if hit == "out" {
			m.ackPrivacyBanner(false)
			m.layoutChat()
			return nil, true
		}
	}
	if m.busy && m.stopBtnX >= 0 && y == m.turnStatusY &&
		msg.X >= m.stopBtnX && msg.X < m.stopBtnX+m.stopBtnW {
		return m.requestCancelTurn(), true
	}
	if m.lastRail != nil {
		if hit := m.lastRail.hit(msg.X, y); hit != timelineHitNone {
			m.jumpTimelineHit(hit)
			return nil, true
		}
	}
	promptY := m.height - 6
	if m.compactUI {
		promptY = m.height - 5
	}
	if y >= promptY {
		m.focusScroll = false
		m.ta.Focus()
		return nil, true
	}
	if y <= 2 {
		return nil, false
	}
	m.focusScroll = true
	m.ta.Blur()
	if len(m.blocks) > 0 {
		rel := y - 3
		if rel < 0 {
			rel = 0
		}
		idx := rel
		if idx >= len(m.blocks) {
			idx = len(m.blocks) - 1
		}
		m.selectedBlock = idx
		if m.groupTools {
			if run, ok := m.runForBlock(idx); ok && !m.groupExpanded(run) {
				m.toggleGroup(run)
			}
		}
		if m.mermaidOn() {
			if src := mermaidSourceFromBlock(m.blocks[idx]); src != "" {
				_, btns := layoutMermaidAffordance(m.mermaidPendingFor(src))
				if kind, ok := hitMermaidButton(msg.X, btns); ok {
					return m.clickMermaid(src, kind), true
				}
			}
		}
		m.refreshVP()
	}
	return nil, true
}

func (m *model) handleMouseWheel(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.overlay != overlayNone && !m.isPromptOverlay() {
		return nil, true
	}
	if m.screen != screenChat {
		return nil, false
	}
	delta := 3
	up := msg.Button == tea.MouseButtonWheelUp
	if m.cfg.UI.InvertScroll {
		up = !up
	}
	if up {
		m.vp.LineUp(delta)
		m.logScroll("flush", "event", -delta)
	} else {
		m.vp.LineDown(delta)
		m.logScroll("flush", "event", delta)
	}
	return nil, true
}

func (m *model) hitPrivacy(x, y int) string {
	if m.privacyY < 0 || !m.showPrivacyBanner() {
		return ""
	}
	lines := strings.Split(strings.TrimRight(stripANSI(privacyBanner()), "\n"), "\n")
	rel := y - m.privacyY
	if rel < 0 || rel >= len(lines) {
		return ""
	}
	line := lines[rel]
	if hitLabel(line, "[Opt in]", x) {
		return "in"
	}
	if hitLabel(line, "[Opt out]", x) {
		return "out"
	}
	return ""
}

func hitLabel(line, label string, x int) bool {
	i := strings.Index(line, label)
	if i < 0 {
		return false
	}
	start := utf8.RuneCountInString(line[:i])
	w := utf8.RuneCountInString(label)
	return x >= start && x < start+w
}
