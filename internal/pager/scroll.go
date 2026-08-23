package pager

import tea "github.com/charmbracelet/bubbletea"

// handleScrollKey is the vim-style scrollback walker (j/k, y copy, e expand)
// matching the Rust pager when the prompt is blurred.
func (m *model) handleScrollKey(msg tea.KeyMsg) tea.Cmd {
	if len(m.blocks) == 0 {
		return nil
	}
	if m.selectedBlock < 0 {
		m.selectedBlock = len(m.blocks) - 1
	}
	switch msg.String() {
	case "j", "down":
		if m.selectedBlock+1 < len(m.blocks) {
			m.selectedBlock++
		}
		m.scrollToBlock(m.selectedBlock)
	case "k", "up":
		if m.selectedBlock > 0 {
			m.selectedBlock--
		}
		m.scrollToBlock(m.selectedBlock)
	case "g":
		m.selectedBlock = 0
		m.vp.GotoTop()
	case "G":
		m.selectedBlock = len(m.blocks) - 1
		m.vp.GotoBottom()
	case "y":
		return m.copyBlock(m.selectedBlock)
	case "e", "enter":
		m.toggleFold(m.selectedBlock)
	case "i", " ":
		if m.vimMode {
			m.focusScroll = false
			m.ta.Focus()
		}
	}
	m.refreshVP()
	return nil
}

func (m *model) toggleFold(idx int) {
	if idx < 0 {
		idx = m.selectedBlock
	}
	if idx < 0 || idx >= len(m.blocks) {
		if n := len(m.blocks); n > 0 {
			idx = n - 1
		} else {
			return
		}
	}
	m.blocks[idx].folded = !m.blocks[idx].folded
	m.refreshVP()
}

func (m *model) jumpFind(key string) {
	for i, b := range m.blocks {
		if b.body == key {
			m.selectedBlock = i
			m.focusScroll = true
			m.ta.Blur()
			m.scrollToBlock(i)
			m.refreshVP()
			return
		}
	}
}
