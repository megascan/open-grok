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
		line := fmt.Sprintf("%d  %s", i+1, o)
		if i == m.qCursor {
			line = selStyle.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n  z  " + dimStyle.Render("free text: "+m.qFree) + "\n")
	b.WriteString("\n" + dimStyle.Render("1–9 pick  ·  enter  ·  shift+x dismiss  ·  esc park"))
	return modalBox.Width(w).Render(b.String())
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
	if m.qFree != "" || s == "z" {
		if s == "z" && m.qFree == "" {
			m.qFree = " "
			m.qFree = ""
			return nil
		}
		switch msg.Type {
		case tea.KeyEsc:
			m.qFree = ""
		case tea.KeyEnter:
			m.replyQuestion(strings.TrimSpace(m.qFree))
		case tea.KeyBackspace:
			if len(m.qFree) > 0 {
				m.qFree = m.qFree[:len(m.qFree)-1]
			}
		case tea.KeyRunes:
			m.qFree += string(msg.Runes)
		}
		if s == "z" && msg.Type == tea.KeyRunes {
			// start free-text mode
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
	case "tab":
		m.overlay = overlayQuestion
	default:
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) && n <= 9 {
			m.replyQuestion(opts[n-1])
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
