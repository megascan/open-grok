package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleMouse hit-tests last-frame regions: overlays ignore clicks, then
// prompt vs scrollback vs dashboard rows. Matches Rust pane order enough for
// click-to-focus and click-to-select-block.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.overlay != overlayNone && m.overlay != overlayPalette && m.overlay != overlayFiles {
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
		m.refreshVP()
	}
	return nil, true
}
