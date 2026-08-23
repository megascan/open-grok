package pager

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
	idx, err := strconv.Atoi(key)
	if err != nil {
		for i, b := range m.blocks {
			if b.body == key {
				idx = i
				err = nil
				break
			}
		}
	}
	if err != nil || idx < 0 || idx >= len(m.blocks) {
		return
	}
	m.selectedBlock = idx
	m.focusScroll = true
	m.ta.Blur()
	m.scrollToBlock(idx)
	m.refreshVP()
}

func (m *model) openFind(arg string) tea.Cmd {
	if m.sess == nil {
		m.appendSys("No active session")
		return nil
	}
	m.overlay = overlayFind
	m.filter = strings.TrimSpace(arg)
	m.jumpRestore = m.vp.YOffset
	m.jumpRestoreSet = true
	m.rebuildFindItems()
	m.previewFind()
	return nil
}

func (m *model) rebuildFindItems() {
	q := strings.ToLower(m.filter)
	m.items = nil
	for i, b := range m.blocks {
		if q == "" || strings.Contains(strings.ToLower(b.body), q) {
			m.items = append(m.items, listItem{Key: strconv.Itoa(i), Label: truncate(b.body, 70)})
		}
	}
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) previewFind() {
	if m.overlay != overlayFind || m.cursor >= len(m.items) {
		return
	}
	idx, err := strconv.Atoi(m.items[m.cursor].Key)
	if err != nil {
		return
	}
	m.jumpToBlock(idx)
}

func (m *model) commitFind() {
	if m.cursor < len(m.items) {
		m.jumpFind(m.items[m.cursor].Key)
	}
	m.jumpRestoreSet = false
	m.overlay = overlayNone
	m.filter = ""
}
