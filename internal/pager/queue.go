package pager

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) openQueue() {
	m.overlay = overlayQueue
	m.cursor = 0
	m.fillQueue()
}

func (m *model) fillQueue() {
	saved := m.cursor
	m.items = nil
	for i, q := range m.queue {
		lines := strings.Count(q, "\n") + 1
		first := q
		if j := strings.IndexByte(q, '\n'); j >= 0 {
			first = q[:j]
		}
		extra := ""
		if lines > 1 {
			extra = fmt.Sprintf("  (+%d lines)", lines-1)
		}
		m.items = append(m.items, listItem{
			Key:    strconv.Itoa(i),
			Label:  fmt.Sprintf("#%d  %s%s", i+1, truncate(strings.TrimSpace(first), 50), extra),
			Detail: q,
		})
	}
	if len(m.items) == 0 {
		m.items = []listItem{{Label: "(empty)", Detail: "follow-ups queued while a turn is running"}}
	}
	m.cursor = saved
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
}

func (m *model) handleQueueKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "d":
		if m.cursor >= 0 && m.cursor < len(m.queue) {
			m.queue = append(m.queue[:m.cursor], m.queue[m.cursor+1:]...)
			m.fillQueue()
			if m.cursor >= len(m.items) && m.cursor > 0 {
				m.cursor--
			}
		}
		return nil, true
	case "K":
		if m.cursor > 0 && m.cursor < len(m.queue) {
			m.queue[m.cursor], m.queue[m.cursor-1] = m.queue[m.cursor-1], m.queue[m.cursor]
			m.cursor--
			m.fillQueue()
		}
		return nil, true
	case "J":
		if m.cursor >= 0 && m.cursor+1 < len(m.queue) {
			m.queue[m.cursor], m.queue[m.cursor+1] = m.queue[m.cursor+1], m.queue[m.cursor]
			m.cursor++
			m.fillQueue()
		}
		return nil, true
	case "enter":
		if m.cursor >= 0 && m.cursor < len(m.queue) {
			m.ta.SetValue(m.queue[m.cursor])
			m.queue = append(m.queue[:m.cursor], m.queue[m.cursor+1:]...)
			m.overlay = overlayNone
			m.ta.Focus()
		}
		return nil, true
	}
	return nil, false
}
