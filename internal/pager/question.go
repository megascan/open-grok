package pager

import (
	"fmt"
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

// splitQuestionLabelDesc splits on a paragraph break (`\n\n`), matching
// Rust `split_question_label_desc`. No break → the full text is the label.
func splitQuestionLabelDesc(text string) (label, desc string) {
	if pos := strings.Index(text, "\n\n"); pos >= 0 {
		return strings.TrimSpace(text[:pos]), strings.TrimSpace(text[pos+2:])
	}
	return strings.TrimSpace(text), ""
}

func radioMarker(selected bool) string {
	if selected {
		return "(" + pickerDotFilled + ")"
	}
	return "(" + pickerDotEmpty + ")"
}

func checkMarker(selected bool) string {
	if selected {
		return "[x]"
	}
	return "[ ]"
}

func (m *model) questionLocal() bool {
	return m.question != nil && m.question.Kind != ""
}

func (m *model) ensureQuestionState() {
	if m.question == nil {
		return
	}
	n := len(m.question.items())
	if n == 0 {
		n = 1
	}
	if len(m.qSel) == n && len(m.qCursors) == n && len(m.qFrees) == n && len(m.qMulti) == n && len(m.qFreeSel) == n {
		return
	}
	m.qSel = make([]int, n)
	m.qCursors = make([]int, n)
	m.qFrees = make([]string, n)
	m.qFreeSel = make([]bool, n)
	m.qMulti = make([]map[int]struct{}, n)
	for i := range m.qSel {
		m.qSel[i] = -1
		m.qMulti[i] = map[int]struct{}{}
	}
	m.qCursor = 0
	m.qFree = ""
	m.qTyping = false
}

func (m *model) persistQuestionTab() {
	if m.question == nil {
		return
	}
	m.ensureQuestionState()
	idx := idxSafe(m.question.Index, len(m.qCursors))
	m.qCursors[idx] = m.qCursor
	m.qFrees[idx] = m.qFree
}

func (m *model) loadQuestionTab() {
	if m.question == nil {
		return
	}
	m.ensureQuestionState()
	idx := idxSafe(m.question.Index, len(m.qCursors))
	m.qCursor = m.qCursors[idx]
	m.qFree = m.qFrees[idx]
	m.qTyping = false
}

func (m *model) optionSelected(optIdx int) bool {
	if m.question == nil {
		return false
	}
	m.ensureQuestionState()
	qIdx := idxSafe(m.question.Index, len(m.qSel))
	if m.question.current().Multi {
		_, ok := m.qMulti[qIdx][optIdx]
		return ok
	}
	return m.qSel[qIdx] == optIdx
}

func (m *model) selectQuestionOption(optIdx int) {
	m.ensureQuestionState()
	qIdx := idxSafe(m.question.Index, len(m.qSel))
	cur := m.question.current()
	if cur.Multi {
		m.qMulti[qIdx][optIdx] = struct{}{}
	} else {
		m.qSel[qIdx] = optIdx
		m.qFreeSel[qIdx] = false
	}
}

func (m *model) toggleQuestionOption(optIdx int) {
	m.ensureQuestionState()
	qIdx := idxSafe(m.question.Index, len(m.qSel))
	cur := m.question.current()
	if cur.Multi {
		if _, ok := m.qMulti[qIdx][optIdx]; ok {
			delete(m.qMulti[qIdx], optIdx)
		} else {
			m.qMulti[qIdx][optIdx] = struct{}{}
		}
		return
	}
	if m.qSel[qIdx] == optIdx {
		m.qSel[qIdx] = -1
	} else {
		m.qSel[qIdx] = optIdx
		m.qFreeSel[qIdx] = false
	}
}

func (m *model) switchQuestion(delta int) {
	if m.question == nil {
		return
	}
	n := len(m.question.items())
	if n <= 1 {
		return
	}
	m.persistQuestionTab()
	next := m.question.Index + delta
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	m.question.Index = next
	m.loadQuestionTab()
}

func (m *model) renderQuestion(w int) string {
	if m.question == nil {
		return ""
	}
	m.ensureQuestionState()
	cur := m.question.current()
	var b strings.Builder
	writeQ := func(s string) {
		b.WriteString(titleStyle.Render(permAccentBar) + "  " + s + "\n")
	}
	label, desc := splitQuestionLabelDesc(cur.Question)
	writeQ(titleStyle.Render(label))
	if desc != "" {
		writeQ("")
		clipped := truncateLines(desc, maxQuestionDescLines)
		for _, line := range strings.Split(clipped, "\n") {
			writeQ(dimStyle.Render(line))
		}
		if strings.Count(desc, "\n")+1 > maxQuestionDescLines {
			writeQ(truncationIndicator())
		}
	}
	writeQ("")
	opts := cur.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	local := m.questionLocal()
	rowCount := len(opts)
	if !local {
		rowCount++
	}
	if m.qCursor >= rowCount {
		m.qCursor = rowCount - 1
	}
	if m.qCursor < 0 {
		m.qCursor = 0
	}
	for i, o := range opts {
		key := optionHotkey(i)
		selected := m.optionSelected(i)
		var marker string
		if cur.Multi {
			marker = checkMarker(selected)
		} else {
			marker = radioMarker(selected)
		}
		label := o
		if utf8.RuneCountInString(label) > 80 {
			runes := []rune(label)
			label = string(runes[:80]) + "…"
		}
		line := fmt.Sprintf("%s %s %s", key, marker, label)
		if i == m.qCursor {
			line = selStyle.Render(line)
		}
		writeQ(line)
	}
	if !local {
		qIdx := idxSafe(m.question.Index, len(m.qFreeSel))
		hasText := strings.TrimSpace(m.qFree) != ""
		selected := m.qFreeSel[qIdx] && hasText
		marker := radioMarker(selected)
		if cur.Multi {
			marker = checkMarker(selected)
		}
		var freeLabel string
		if hasText {
			first := m.qFree
			if j := strings.IndexByte(first, '\n'); j >= 0 {
				first = first[:j]
			}
			if utf8.RuneCountInString(first) > 50 {
				first = string([]rune(first)[:50]) + "…"
			}
			if m.qTyping && m.qCursor == len(opts) {
				freeLabel = promptArrow + first + "█"
			} else {
				freeLabel = promptArrow + first
			}
		} else if m.qTyping && m.qCursor == len(opts) {
			freeLabel = questionFreeformPlaceholder + "█"
		} else {
			freeLabel = questionFreeformPlaceholder
		}
		otherLine := fmt.Sprintf("z %s %s", marker, freeLabel)
		if m.qCursor == len(opts) {
			otherLine = selStyle.Render(otherLine)
		} else if hasText && !selected {
			otherLine = dimStyle.Render(otherLine)
		}
		writeQ(otherLine)
	}
	_ = maxQuestionPreviewLines
	return modalBox.Width(w).Render(b.String())
}

func optionHotkey(i int) string {
	if i < 9 {
		return string(rune('1' + i))
	}
	if i < 35 {
		return string(rune('a' + (i - 9)))
	}
	return " "
}

func optionIndexFromKey(s string, n int) int {
	if len(s) != 1 {
		return -1
	}
	c := s[0]
	var idx int
	switch {
	case c >= '1' && c <= '9':
		idx = int(c - '1')
	case c >= 'a' && c <= 'f':
		idx = 9 + int(c-'a')
	default:
		return -1
	}
	if idx < n {
		return idx
	}
	return -1
}

func (m *model) handleQuestionKey(msg tea.KeyMsg) tea.Cmd {
	if m.question == nil {
		return nil
	}
	m.ensureQuestionState()
	cur := m.question.current()
	opts := cur.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	local := m.questionLocal()
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
		case tea.KeyEsc, tea.KeyCtrlC:
			m.qTyping = false
		case tea.KeyEnter:
			m.qTyping = false
			m.markFreeformSelected()
			return m.advanceOrSubmitQuestion()
		case tea.KeyBackspace:
			if len(m.qFree) > 0 {
				m.qFree = m.qFree[:len(m.qFree)-1]
			}
		case tea.KeyRunes:
			m.qFree += string(msg.Runes)
		}
		return nil
	}
	onFreeform := !local && m.qCursor == len(opts)
	if onFreeform && s != " " && s != "up" && s != "down" && s != "k" && s != "j" &&
		s != "tab" && s != "shift+tab" && s != "left" && s != "right" && s != "h" && s != "l" &&
		s != "[" && s != "]" && s != "enter" && s != "esc" && s != "shift+x" &&
		s != "ctrl+c" && s != "ctrl+f" && msg.Type == tea.KeyRunes {
		m.qTyping = true
		m.qFree += string(msg.Runes)
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
		m.switchQuestion(-1)
	case "right", "l", "]":
		m.switchQuestion(1)
	case " ":
		if onFreeform {
			m.qTyping = true
			return nil
		}
		if m.qCursor >= 0 && m.qCursor < len(opts) {
			m.toggleQuestionOption(m.qCursor)
		}
	case "enter":
		if onFreeform {
			m.qTyping = true
			return nil
		}
		if m.qCursor >= 0 && m.qCursor < len(opts) {
			m.selectQuestionOption(m.qCursor)
			return m.advanceOrSubmitQuestion()
		}
	case "shift+x", "ctrl+c":
		return m.replyQuestion("")
	case "ctrl+f":
		// Fullscreen is a no-op in the Go pager; keep the binding so the
		// card does not leak the chord into the composer.
	case "esc":
		m.overlay = overlayNone
		m.focusScroll = true
		m.ta.Blur()
	case "g":
		m.qCursor = 0
	case "G":
		m.qCursor = rowCount - 1
	default:
		if idx := optionIndexFromKey(s, len(opts)); idx >= 0 {
			m.qCursor = idx
			m.selectQuestionOption(idx)
			return m.advanceOrSubmitQuestion()
		}
	}
	return nil
}

func (m *model) markFreeformSelected() {
	m.ensureQuestionState()
	qIdx := idxSafe(m.question.Index, len(m.qFreeSel))
	if strings.TrimSpace(m.qFree) == "" {
		m.qFreeSel[qIdx] = false
		return
	}
	m.qFreeSel[qIdx] = true
	m.qFrees[qIdx] = m.qFree
	if !m.question.current().Multi {
		m.qSel[qIdx] = -1
	}
}

func (m *model) advanceOrSubmitQuestion() tea.Cmd {
	if m.question == nil {
		return nil
	}
	n := len(m.question.items())
	if m.question.Index+1 < n {
		m.switchQuestion(1)
		return nil
	}
	return m.submitQuestionAnswers()
}

func (m *model) selectedLabels(qIdx int) []string {
	items := m.question.items()
	if qIdx < 0 || qIdx >= len(items) {
		return nil
	}
	item := items[qIdx]
	opts := item.Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	if item.Multi {
		var out []string
		for i, o := range opts {
			if _, ok := m.qMulti[qIdx][i]; ok {
				out = append(out, o)
			}
		}
		return out
	}
	if qIdx < len(m.qSel) && m.qSel[qIdx] >= 0 && m.qSel[qIdx] < len(opts) {
		return []string{opts[m.qSel[qIdx]]}
	}
	return nil
}

func (m *model) submitQuestionAnswers() tea.Cmd {
	if m.question == nil {
		return nil
	}
	m.persistQuestionTab()
	items := m.question.items()
	if len(items) == 1 {
		labels := m.selectedLabels(0)
		if len(labels) > 0 {
			return m.replyQuestion(strings.Join(labels, ", "))
		}
		if m.qFreeSel[0] && strings.TrimSpace(m.qFrees[0]) != "" {
			return m.replyQuestion(strings.TrimSpace(m.qFrees[0]))
		}
		return m.replyQuestion("")
	}
	var parts []string
	for i := range items {
		labels := m.selectedLabels(i)
		note := ""
		if i < len(m.qFreeSel) && m.qFreeSel[i] {
			note = strings.TrimSpace(m.qFrees[i])
		}
		if len(labels) == 0 && note != "" {
			labels = []string{"Other"}
		}
		line := strings.Join(labels, ", ")
		if note != "" {
			if line != "" {
				line += " — " + note
			} else {
				line = note
			}
		}
		parts = append(parts, line)
	}
	return m.replyQuestion(strings.Join(parts, "\n"))
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
	m.qSel = nil
	m.qMulti = nil
	m.qFrees = nil
	m.qFreeSel = nil
	m.qCursors = nil
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
