package pager

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) renderQuestion(w int) string {
	if m.question == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Question") + "\n\n")
	b.WriteString(m.question.Question + "\n\n")
	opts := m.question.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	for i, o := range opts {
		key := optionHotkey(i)
		line := fmt.Sprintf("%s  %s", key, o)
		if i == m.qCursor {
			line = selStyle.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n  z  " + dimStyle.Render("free text: "+m.qFree) + "\n")
	b.WriteString("\n" + dimStyle.Render("1–9 a–f pick  ·  tab wrap  ·  enter  ·  shift+x dismiss  ·  esc park"))
	return modalBox.Width(w).Render(b.String())
}

func optionHotkey(i int) string {
	if i < 9 {
		return strconv.Itoa(i + 1)
	}
	if i < 15 {
		return string(rune('a' + (i - 9)))
	}
	return strconv.Itoa(i + 1)
}

func optionIndexFromKey(s string, n int) int {
	if d, err := strconv.Atoi(s); err == nil && d >= 1 && d <= 9 && d <= n {
		return d - 1
	}
	if len(s) == 1 && s[0] >= 'a' && s[0] <= 'f' {
		idx := 9 + int(s[0]-'a')
		if idx < n {
			return idx
		}
	}
	return -1
}

func (m *model) handleQuestionKey(msg tea.KeyMsg) tea.Cmd {
	if m.question == nil {
		return nil
	}
	opts := m.question.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	s := msg.String()
	if s == "z" && !m.qTyping {
		m.qTyping = true
		return nil
	}
	if m.qTyping {
		switch msg.Type {
		case tea.KeyEsc:
			m.qTyping = false
			m.qFree = ""
		case tea.KeyEnter:
			m.qTyping = false
			m.replyQuestion(strings.TrimSpace(m.qFree))
		case tea.KeyBackspace:
			if len(m.qFree) > 0 {
				m.qFree = m.qFree[:len(m.qFree)-1]
			}
		case tea.KeyRunes:
			m.qFree += string(msg.Runes)
		}
		return nil
	}
	switch s {
	case "up", "k":
		if m.qCursor > 0 {
			m.qCursor--
		}
	case "down", "j":
		if m.qCursor+1 < len(opts) {
			m.qCursor++
		}
	case "tab":
		m.qCursor = (m.qCursor + 1) % len(opts)
	case "shift+tab":
		if m.qCursor == 0 {
			m.qCursor = len(opts) - 1
		} else {
			m.qCursor--
		}
	case "left", "h", "[":
		// previous question — single-question card is a no-op wrap
	case "right", "l", "]":
		// next question — single-question card is a no-op wrap
	case "enter":
		if m.qCursor >= 0 && m.qCursor < len(opts) {
			m.replyQuestion(opts[m.qCursor])
		}
	case "shift+x":
		m.replyQuestion("")
	case "esc":
		m.overlay = overlayNone
		m.focusScroll = true
		m.ta.Blur()
	default:
		if idx := optionIndexFromKey(s, len(opts)); idx >= 0 {
			m.replyQuestion(opts[idx])
		}
	}
	return nil
}

func (m *model) replyQuestion(ans string) {
	if m.question == nil {
		return
	}
	ch := m.question.Reply
	m.question = nil
	if m.overlay == overlayQuestion {
		m.overlay = overlayNone
	}
	if ch != nil {
		ch <- ans
	}
}
