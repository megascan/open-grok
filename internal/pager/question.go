package pager

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

type questionItem struct {
	Question string
	Options  []string
	Multi    bool
}

func (msg *qAskMsg) items() []questionItem {
	if msg == nil {
		return nil
	}
	if len(msg.Questions) > 0 {
		return msg.Questions
	}
	opts := msg.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	return []questionItem{{Question: msg.Question, Options: opts}}
}

func (msg *qAskMsg) current() questionItem {
	items := msg.items()
	if len(items) == 0 {
		return questionItem{Question: "?", Options: []string{"yes", "no"}}
	}
	if msg.Index < 0 || msg.Index >= len(items) {
		return items[0]
	}
	return items[idxSafe(msg.Index, len(items))]
}

func idxSafe(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

const (
	maxQuestionDescLines    = 5
	maxQuestionPreviewLines = 6
)

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n…"
}

func (m *model) renderQuestion(w int) string {
	if m.question == nil {
		return ""
	}
	items := m.question.items()
	cur := m.question.current()
	var b strings.Builder
	title := "Question"
	if len(items) > 1 {
		title = fmt.Sprintf("Question %d/%d", m.question.Index+1, len(items))
	}
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString(truncateLines(cur.Question, maxQuestionDescLines) + "\n\n")
	opts := cur.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	local := m.question != nil && m.question.Kind != ""
	rowCount := len(opts)
	if !local {
		rowCount++ // sticky Other
	}
	if m.qCursor >= rowCount {
		m.qCursor = rowCount - 1
	}
	for i, o := range opts {
		key := optionHotkey(i)
		marker := "(○)"
		if i == m.qCursor {
			marker = "(●)"
		}
		label := o
		if utf8.RuneCountInString(label) > 80 {
			runes := []rune(label)
			label = string(runes[:80]) + "…"
		}
		line := fmt.Sprintf(" %s %s %s", key, marker, label)
		if i == m.qCursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if !local {
		otherMarker := "(○)"
		otherLabel := "Other"
		if m.qCursor == len(opts) {
			otherMarker = "(●)"
			if m.qTyping {
				otherLabel = "Other  ❯ " + m.qFree + "█"
			} else if m.qFree != "" {
				otherLabel = "Other  ❯ " + m.qFree
			}
		} else if m.qFree != "" {
			otherLabel = "Other  ❯ " + m.qFree
		}
		otherLine := fmt.Sprintf(" z %s %s", otherMarker, otherLabel)
		if m.qCursor == len(opts) {
			otherLine = selStyle.Render(otherLine)
		}
		b.WriteString(otherLine + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("1–9 a–f pick  ·  j/k  ·  tab wrap  ·  enter  ·  ←/→ questions  ·  shift+x dismiss  ·  esc park"))
	_ = maxQuestionPreviewLines
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
	cur := m.question.current()
	opts := cur.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	local := m.question.Kind != ""
	rowCount := len(opts)
	if !local {
		rowCount++
	}
	s := msg.String()
	if s == "z" && !m.qTyping && !local {
		m.qCursor = len(opts)
		m.qTyping = true
		return nil
	}
	if m.qTyping {
		switch msg.Type {
		case tea.KeyEsc:
			m.qTyping = false
		case tea.KeyEnter:
			m.qTyping = false
			return m.replyQuestion(strings.TrimSpace(m.qFree))
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
		if m.qCursor+1 < rowCount {
			m.qCursor++
		}
	case "tab":
		m.qCursor = (m.qCursor + 1) % rowCount
	case "shift+tab":
		if m.qCursor == 0 {
			m.qCursor = rowCount - 1
		} else {
			m.qCursor--
		}
	case "left", "h", "[":
		if n := len(m.question.items()); n > 1 {
			m.question.Index = (m.question.Index + n - 1) % n
			m.qCursor = 0
			m.qFree = ""
		}
	case "right", "l", "]":
		if n := len(m.question.items()); n > 1 {
			m.question.Index = (m.question.Index + 1) % n
			m.qCursor = 0
			m.qFree = ""
		}
	case "enter":
		if !local && m.qCursor == len(opts) {
			m.qTyping = true
			return nil
		}
		if m.qCursor >= 0 && m.qCursor < len(opts) {
			return m.replyQuestion(opts[m.qCursor])
		}
	case "shift+x":
		return m.replyQuestion("")
	case "esc":
		m.overlay = overlayNone
		m.focusScroll = true
		m.ta.Blur()
	default:
		if idx := optionIndexFromKey(s, len(opts)); idx >= 0 {
			return m.replyQuestion(opts[idx])
		}
	}
	return nil
}

func (m *model) replyQuestion(ans string) tea.Cmd {
	if m.question == nil {
		return nil
	}
	kind := m.question.Kind
	directive := m.question.Directive
	ch := m.question.Reply
	m.question = nil
	m.qTyping = false
	m.qFree = ""
	m.qCursor = 0
	if m.overlay == overlayQuestion {
		m.overlay = overlayNone
	}
	if kind == "fork-worktree" {
		return m.resolveForkWorktree(ans, directive)
	}
	if kind == "new-worktree" {
		return m.resolveNewWorktree(ans)
	}
	if ch != nil {
		ch <- ans
	}
	return nil
}
