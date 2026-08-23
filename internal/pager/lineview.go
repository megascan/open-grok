package pager

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// lineViewer is the @file:N-M picker (j/k, / search, v visual, enter confirm).
type lineViewer struct {
	Path, Rel, Title string
	Lines            []string
	Cursor           int
	Anchor           int
	Visual           bool
	Filter           string
	FilterOn         bool
	Offset           int
	ApplyDraft       bool
	ReturnTo         overlay
}

func parseLineRange(s string) (start, end int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	a, b, ranged := strings.Cut(s, "-")
	n1, err := strconv.Atoi(a)
	if err != nil || n1 < 1 {
		return 0, 0, false
	}
	if !ranged {
		return n1, n1, true
	}
	n2, err := strconv.Atoi(b)
	if err != nil || n2 < 1 {
		return 0, 0, false
	}
	if n2 < n1 {
		n1, n2 = n2, n1
	}
	return n1, n2, true
}

func splitFileRef(token string) (path, suffix string) {
	token = strings.TrimPrefix(token, "@")
	token = strings.TrimPrefix(token, "!")
	if i := strings.LastIndexByte(token, ':'); i >= 0 {
		rest := token[i+1:]
		if _, _, ok := parseLineRange(rest); ok {
			return token[:i], ":" + rest
		}
	}
	return token, ""
}

func lineRangeSuffix(start, end int) string {
	if start <= 0 {
		return ""
	}
	if end <= start {
		return ":" + strconv.Itoa(start)
	}
	return ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

func countSourceLines(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if strings.HasSuffix(content, "\n") {
		return n
	}
	return n + 1
}

func splitSourceLines(content string) []string {
	n := countSourceLines(content)
	raw := strings.Split(content, "\n")
	if len(raw) > n {
		raw = raw[:n]
	}
	return raw
}

func (v *lineViewer) selectedRange() (start, end int) {
	if len(v.Lines) == 0 {
		return 0, 0
	}
	if !v.Visual {
		n := v.Cursor + 1
		return n, n
	}
	a, b := v.Anchor, v.Cursor
	if a > b {
		a, b = b, a
	}
	return a + 1, b + 1
}

func (v *lineViewer) suffix() string {
	s, e := v.selectedRange()
	return lineRangeSuffix(s, e)
}

func (v *lineViewer) visibleIndices() []int {
	if v.Filter == "" {
		idx := make([]int, len(v.Lines))
		for i := range v.Lines {
			idx[i] = i
		}
		return idx
	}
	q := strings.ToLower(v.Filter)
	var idx []int
	for i, line := range v.Lines {
		if strings.Contains(strings.ToLower(line), q) {
			idx = append(idx, i)
		}
	}
	return idx
}

func (m *model) openLineViewerFile(rel string, applyDraft bool) tea.Cmd {
	full := rel
	if !filepath.IsAbs(rel) {
		full = filepath.Join(m.opts.CWD, rel)
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		return nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		m.appendSys("cannot open " + rel + ": " + err.Error())
		return nil
	}
	return m.openLineViewerContent(full, rel, string(data), applyDraft)
}

func (m *model) openLineViewerContent(path, title, content string, applyDraft bool) tea.Cmd {
	prev := m.overlay
	v := &lineViewer{
		Path: path, Rel: title, Title: title,
		Lines:      splitSourceLines(content),
		ApplyDraft: applyDraft, ReturnTo: prev,
	}
	if v.Rel == "" {
		v.Rel = filepath.Base(path)
		v.Title = v.Rel
	}
	m.lineView = v
	m.overlay = overlayLineView
	return nil
}

func (m *model) renderLineViewer(w int) string {
	v := m.lineView
	if v == nil {
		return ""
	}
	w = min(120, max(44, m.width-6))
	h := max(8, min(22, m.height-8))
	s, e := v.selectedRange()
	rangeHint := v.suffix()
	if rangeHint == "" {
		rangeHint = "(none)"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(v.Title) + "  " + dimStyle.Render(rangeHint) + "\n")
	if v.FilterOn || v.Filter != "" {
		q := v.Filter
		if v.FilterOn {
			q += "█"
		}
		b.WriteString(dimStyle.Render("/") + q + "\n")
		h--
	}
	digits := len(strconv.Itoa(max(1, len(v.Lines))))
	if v.Cursor < v.Offset {
		v.Offset = v.Cursor
	}
	if v.Cursor >= v.Offset+h {
		v.Offset = v.Cursor - h + 1
	}
	a, z := v.Anchor, v.Cursor
	if a > z {
		a, z = z, a
	}
	for i := v.Offset; i < len(v.Lines) && i < v.Offset+h; i++ {
		num := fmtPad(i+1, digits)
		text := v.Lines[i]
		inSel := v.Visual && i >= a && i <= z
		line := dimStyle.Render(num+" ") + text
		if i == v.Cursor && !inSel {
			line = selStyle.Render(num + " " + text)
		} else if inSel {
			line = selStyle.Render(num + " " + text)
		}
		b.WriteString(line + "\n")
	}
	if len(v.Lines) == 0 {
		b.WriteString(dimStyle.Render("(empty file)") + "\n")
	}
	footer := "j/k nav  ·  / search  ·  v visual  ·  enter confirm  ·  esc"
	if v.FilterOn {
		footer = "type to search  ·  esc exit search"
	} else if v.Visual {
		footer = "j/k extend  ·  enter " + v.suffix() + "  ·  v/esc cancel visual"
	}
	if !v.ApplyDraft {
		footer = "j/k nav  ·  / search  ·  y copy  ·  esc close"
	}
	b.WriteString("\n" + dimStyle.Render(footer))
	_ = s
	_ = e
	return modalBox.Width(w).Render(b.String())
}

func fmtPad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = " " + s
	}
	return s
}

func (m *model) confirmLineViewer() tea.Cmd {
	v := m.lineView
	if v == nil {
		return nil
	}
	suffix := v.suffix()
	ret := v.ReturnTo
	rel := v.Rel
	apply := v.ApplyDraft
	m.lineView = nil
	if ret == overlayLineView {
		ret = overlayNone
	}
	m.overlay = ret
	if apply {
		m.applyLineRangeToDraft(rel, suffix)
	}
	if m.overlay == overlayAgents && m.agents != nil {
		return nil
	}
	if m.overlay == overlayNone {
		m.ta.Focus()
	}
	return nil
}

func (m *model) tryOpenLineViewerFromDraft() (tea.Cmd, bool) {
	path := ""
	suffix := ""
	draft := m.ta.Value()
	if tok, ok := parseAtToken(draft); ok {
		path, suffix = splitFileRef(tok.Query)
	} else {
		trimmed := strings.TrimRightFunc(draft, unicode.IsSpace)
		i := strings.LastIndexByte(trimmed, '@')
		if i < 0 {
			return nil, false
		}
		if i > 0 {
			r := rune(trimmed[i-1])
			if !unicode.IsSpace(r) && r != '\n' && r != '\t' {
				return nil, false
			}
		}
		rest := trimmed[i+1:]
		if strings.ContainsAny(rest, " \t\n") {
			return nil, false
		}
		path, suffix = splitFileRef(rest)
	}
	if path == "" {
		return nil, false
	}
	full := path
	if !filepath.IsAbs(path) {
		full = filepath.Join(m.opts.CWD, path)
	}
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		return nil, false
	}
	start, end, _ := parseLineRange(strings.TrimPrefix(suffix, ":"))
	cmd := m.openLineViewerFile(path, true)
	if m.lineView == nil {
		return nil, false
	}
	if start > 0 {
		m.lineView.Cursor = start - 1
		if end > start {
			m.lineView.Visual = true
			m.lineView.Anchor = start - 1
			m.lineView.Cursor = end - 1
		}
	}
	return cmd, true
}

func (m *model) applyLineRangeToDraft(rel, suffix string) {
	draft := m.ta.Value()
	tok, ok := parseAtToken(draft)
	if ok {
		path, _ := splitFileRef(tok.Query)
		if path == "" {
			path = rel
		}
		prefix := draft[:tok.Start]
		m.ta.SetValue(prefix + "@" + path + suffix + " ")
		return
	}
	// Cursor sits after a completed @path (with trailing space).
	trimmed := strings.TrimRightFunc(draft, unicode.IsSpace)
	if i := strings.LastIndexByte(trimmed, '@'); i >= 0 {
		pre := trimmed[:i]
		rest := trimmed[i+1:]
		if !strings.ContainsAny(rest, " \t\n") {
			path, _ := splitFileRef(rest)
			m.ta.SetValue(pre + "@" + path + suffix + " ")
			return
		}
	}
	m.ta.SetValue(strings.TrimRight(draft, " ") + "@" + rel + suffix + " ")
}

func (m *model) handleLineViewerKey(msg tea.KeyMsg) tea.Cmd {
	v := m.lineView
	if v == nil {
		m.overlay = overlayNone
		return nil
	}
	key := msg.String()
	n := len(v.Lines)
	if v.FilterOn {
		switch key {
		case "esc":
			v.FilterOn = false
		case "down":
			if v.Cursor+1 < n {
				v.Cursor++
			}
		case "up":
			if v.Cursor > 0 {
				v.Cursor--
			}
		case "enter":
			v.FilterOn = false
			if v.ApplyDraft {
				return m.confirmLineViewer()
			}
		case "backspace":
			v.Filter = trimLastRune(v.Filter)
		default:
			if msg.Type == tea.KeyRunes {
				v.Filter += string(msg.Runes)
			}
		}
		return nil
	}
	switch key {
	case "esc":
		if v.Visual {
			v.Visual = false
			return nil
		}
		ret := v.ReturnTo
		m.lineView = nil
		if ret == overlayLineView || ret == overlayFiles {
			ret = overlayNone
		}
		m.overlay = ret
	case "down", "j":
		if v.Cursor+1 < n {
			v.Cursor++
		}
	case "up", "k":
		if v.Cursor > 0 {
			v.Cursor--
		}
	case "pgdown":
		v.Cursor = min(n-1, v.Cursor+10)
	case "pgup":
		v.Cursor = max(0, v.Cursor-10)
	case "g":
		v.Cursor = 0
	case "G":
		if n > 0 {
			v.Cursor = n - 1
		}
	case "v":
		if v.Visual {
			v.Visual = false
		} else {
			v.Visual = true
			v.Anchor = v.Cursor
		}
	case "/", "i":
		v.FilterOn = true
	case "y":
		s, e := v.selectedRange()
		if s > 0 && e <= len(v.Lines) {
			return m.copyText(strings.Join(v.Lines[s-1:e], "\n"), false)
		}
	case "enter", "x":
		if v.ApplyDraft || key == "enter" {
			return m.confirmLineViewer()
		}
	}
	return nil
}
