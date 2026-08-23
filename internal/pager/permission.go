package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) renderPermission(w int) string {
	if m.perm == nil {
		return ""
	}
	park := ""
	if m.permParked {
		park = "\n" + dimStyle.Render("keyboard parked in scrollback — Tab returns")
	}
	body := titleStyle.Render("Permission") + "\n\n" +
		"Allow " + toolStyle.Render(m.perm.Tool) + " (" + m.perm.Access + ")?\n"
	if m.perm.Path != "" {
		body += dimStyle.Render("path: "+m.perm.Path) + "\n"
	}
	body += "\n" +
		selStyle.Render(" y ") + " allow once   " +
		selStyle.Render(" a ") + " always this tool   " +
		selStyle.Render(" n ") + " deny\n" +
		dimStyle.Render("esc parks in scrollback") + park
	return modalBox.Width(w).Render(body)
}

func (m *model) handlePermissionKey(msg tea.KeyMsg) tea.Cmd {
	if m.perm == nil {
		return nil
	}
	switch msg.String() {
	case "y", "enter":
		m.replyPerm(true, false)
	case "a":
		m.replyPerm(true, true)
	case "n":
		m.replyPerm(false, false)
	case "esc":
		m.permParked = true
		m.overlay = overlayNone
		m.focusScroll = true
		m.ta.Blur()
	case "tab":
		// stay in card
	}
	return nil
}

func (m *model) replyPerm(allow, always bool) {
	if m.perm == nil {
		return
	}
	if always && m.sess != nil {
		m.sess.Allow = append(m.sess.Allow, m.perm.Tool)
		if m.cfg.UI.RememberApprovals {
			m.appendSys("always allow " + m.perm.Tool + " (this session + remembered for the project)")
		}
	}
	ch := m.perm.Reply
	m.perm = nil
	m.overlay = overlayNone
	m.permParked = false
	if m.question != nil {
		m.overlay = overlayQuestion
	}
	if ch != nil {
		ch <- allow
	}
}

func (m *model) handleScrollKey(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	move := func(d int) {
		if len(m.blocks) == 0 {
			return
		}
		if m.selectedBlock < 0 {
			m.selectedBlock = len(m.blocks) - 1
		}
		m.selectedBlock += d
		if m.selectedBlock < 0 {
			m.selectedBlock = 0
		}
		if m.selectedBlock >= len(m.blocks) {
			m.selectedBlock = len(m.blocks) - 1
		}
		if s == "j" || s == "down" {
			m.vp.LineDown(1)
		}
		if s == "k" || s == "up" {
			m.vp.LineUp(1)
		}
		m.refreshVP()
	}
	idx := m.selectedBlock
	if m.vimMode {
		switch s {
		case "j":
			move(1)
		case "k":
			move(-1)
		case "g":
			m.selectedBlock = 0
			m.vp.GotoTop()
			m.refreshVP()
		case "G":
			m.selectedBlock = len(m.blocks) - 1
			m.vp.GotoBottom()
			m.refreshVP()
		case "ctrl+d":
			m.vp.HalfViewDown()
		case "ctrl+u":
			m.vp.HalfViewUp()
		case "y":
			return m.copyBlock(idx)
		case "e", "h", "l":
			m.toggleFold(idx)
		case "E":
			m.foldAll()
		case "i", "tab", " ":
			m.focusScroll = false
			m.ta.Focus()
		}
		return nil
	}
	switch s {
	case "up":
		move(-1)
	case "down":
		move(1)
	case "left", "right":
		m.toggleFold(idx)
	case " ":
		m.focusScroll = false
		m.ta.Focus()
	case "y":
		return m.copyBlock(idx)
	}
	return nil
}

func (m *model) toggleFold(idx int) {
	if len(m.blocks) == 0 {
		return
	}
	if idx < 0 {
		idx = len(m.blocks) - 1
	}
	if idx >= len(m.blocks) {
		return
	}
	if m.groupTools {
		if run, ok := m.runForBlock(idx); ok {
			m.toggleGroup(run)
			m.refreshVP()
			return
		}
	}
	m.blocks[idx].folded = !m.blocks[idx].folded
	m.refreshVP()
}

func (m *model) foldAll() {
	all := true
	for _, b := range m.blocks {
		if !b.folded {
			all = false
			break
		}
	}
	for i := range m.blocks {
		m.blocks[i].folded = !all
	}
	m.refreshVP()
}

func (m *model) jumpFind(q string) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if stringsContainsFold(m.blocks[i].body, q) || stringsContainsFold(m.blocks[i].title, q) {
			m.vp.SetContent(m.renderTranscript())
			return
		}
	}
}

func stringsContainsFold(s, q string) bool {
	return q == "" || strings.Contains(strings.ToLower(s), strings.ToLower(q))
}
