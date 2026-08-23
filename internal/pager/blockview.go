package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) renderBlockViewer(w int) string {
	title := m.blockTitle
	if title == "" {
		title = "Block"
	}
	lines := strings.Split(m.blockBody, "\n")
	limit := 18
	start := m.blockScroll
	if start > len(lines) {
		start = max(0, len(lines)-1)
		m.blockScroll = start
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n")
	b.WriteString(dimStyle.Render("ctrl+e expand  ·  j/k scroll  ·  esc close") + "\n\n")
	if len(lines) == 0 {
		b.WriteString(dimStyle.Render("(empty)"))
	} else {
		b.WriteString(codeStyle.Render(strings.Join(lines[start:end], "\n")))
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handleBlockViewerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.perm != nil {
			m.overlay = overlayPermission
			m.permParked = false
		} else {
			m.overlay = overlayNone
		}
	case "up", "k":
		if m.blockScroll > 0 {
			m.blockScroll--
		}
	case "down", "j":
		m.blockScroll++
	}
	return nil
}
