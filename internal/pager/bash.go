package pager

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// permissionCollapsedRows is the Rust PERMISSION_COLLAPSED_ROWS budget.
// When truncated, the last budgeted row is the "... Ctrl-F to expand" hint.
const permissionCollapsedRows = 5

var bashKeywords = map[string]struct{}{
	"if": {}, "then": {}, "else": {}, "elif": {}, "fi": {},
	"for": {}, "while": {}, "until": {}, "do": {}, "done": {},
	"case": {}, "esac": {}, "function": {}, "in": {}, "select": {},
	"time": {}, "coproc": {},
}

// prepareBashDisplayText normalizes a bash command for the permission card.
// Matches Rust permission_view::prepare_bash_display_text:
// unify newlines, trim trailing whitespace per line, keep `\` continuations,
// drop trailing blank lines, and keep a dangling `\` visible without a stray row.
func prepareBashDisplayText(command string) string {
	normalized := strings.ReplaceAll(command, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	var out strings.Builder
	out.Grow(len(normalized))
	for i, line := range strings.Split(normalized, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(strings.TrimRightFunc(line, unicode.IsSpace))
	}
	s := out.String()
	for strings.HasSuffix(s, "\n") {
		without := s[:len(s)-1]
		if strings.HasSuffix(without, "\\") {
			s = without
			break
		}
		if without == "" || strings.HasSuffix(without, "\n") {
			s = without
			continue
		}
		s = without
		break
	}
	return s
}

// bashWords is a quote/heredoc-aware splitter used for ←/→ permission scope.
// It matches Rust sh_split_simple on the command (quotes stripped) and does
// not emit words from heredoc payload, so `cat <<EOF\nhello world\nEOF` scopes
// as `cat <<EOF` rather than splitting the body on spaces.
func bashWords(s string) []string {
	runes := []rune(s)
	var result []string
	var current strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	i := 0
	for i < len(runes) {
		c := runes[i]
		if !inSingle && !inDouble && current.Len() == 0 && c == '<' && i+1 < len(runes) && runes[i+1] == '<' {
			i, result = consumeHeredocOpener(runes, i, result)
			continue
		}
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			i++
		case c == '"' && !inSingle:
			inDouble = !inDouble
			i++
		case c == '\\' && !inSingle:
			i++
			if i < len(runes) {
				current.WriteRune(runes[i])
				i++
			}
		case unicode.IsSpace(c) && !inSingle && !inDouble:
			flush()
			i++
		default:
			current.WriteRune(c)
			i++
		}
	}
	flush()
	return result
}

func consumeHeredocOpener(runes []rune, i int, result []string) (int, []string) {
	i += 2
	dash := false
	if i < len(runes) && runes[i] == '-' {
		dash = true
		i++
	}
	for i < len(runes) && unicode.IsSpace(runes[i]) && runes[i] != '\n' {
		i++
	}
	delim, n := readHeredocDelim(runes[i:])
	i += n
	tok := "<<"
	if dash {
		tok += "-"
	}
	tok += delim
	if delim != "" {
		result = append(result, tok)
	}
	for i < len(runes) && runes[i] != '\n' {
		i++
	}
	if i < len(runes) {
		i++
	}
	for i < len(runes) {
		start := i
		for i < len(runes) && runes[i] != '\n' {
			i++
		}
		line := string(runes[start:i])
		check := line
		if dash {
			check = strings.TrimLeft(line, "\t")
		}
		if check == delim {
			if i < len(runes) {
				i++
			}
			break
		}
		if i < len(runes) {
			i++
		}
	}
	return i, result
}

func readHeredocDelim(runes []rune) (string, int) {
	if len(runes) == 0 {
		return "", 0
	}
	quote := rune(0)
	i := 0
	if runes[0] == '\'' || runes[0] == '"' {
		quote = runes[0]
		i++
	}
	start := i
	for i < len(runes) {
		c := runes[i]
		if quote != 0 {
			if c == quote {
				delim := string(runes[start:i])
				return delim, i + 1
			}
			i++
			continue
		}
		if unicode.IsSpace(c) || c == '<' || c == '>' || c == ';' || c == '|' || c == '&' {
			break
		}
		i++
	}
	return string(runes[start:i]), i
}

func bashDisplayRows(command string, width, maxRows int) (rows []string, truncated bool) {
	text := prepareBashDisplayText(command)
	if text == "" {
		return nil, false
	}
	payload := heredocPayloadRanges(text)
	var physical []string
	offset := 0
	for idx, line := range strings.Split(text, "\n") {
		if idx > 0 {
			offset++
		}
		lineStart := offset
		offset += len(line)
		if width <= 0 || rangeFullyInside(lineStart, lineStart+len(line), payload) {
			physical = append(physical, line)
			continue
		}
		physical = append(physical, wrapPlain(line, width)...)
	}
	if maxRows <= 0 || len(physical) <= maxRows {
		return physical, false
	}
	return physical[:maxRows-1], true
}

func wrapPlain(line string, width int) []string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return []string{line}
	}
	var rows []string
	var b strings.Builder
	col := 0
	for _, r := range line {
		w := 1
		if r == '\t' {
			w = 4
		} else if utf8.RuneLen(r) > 1 {
			w = lipgloss.Width(string(r))
		}
		if col+w > width && b.Len() > 0 {
			rows = append(rows, b.String())
			b.Reset()
			col = 0
		}
		b.WriteRune(r)
		col += w
	}
	if b.Len() > 0 || len(rows) == 0 {
		rows = append(rows, b.String())
	}
	return rows
}

type byteRange struct{ start, end int }

func heredocPayloadRanges(script string) []byteRange {
	runes := []rune(script)
	var ranges []byteRange
	inSingle, inDouble := false, false
	byteAt := func(runeIdx int) int {
		return len(string(runes[:runeIdx]))
	}
	i := 0
	for i < len(runes) {
		c := runes[i]
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			i++
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
			i++
			continue
		}
		if c == '\\' && !inSingle {
			i += 2
			continue
		}
		if !inSingle && !inDouble && c == '<' && i+1 < len(runes) && runes[i+1] == '<' {
			j := i + 2
			dash := false
			if j < len(runes) && runes[j] == '-' {
				dash = true
				j++
			}
			for j < len(runes) && unicode.IsSpace(runes[j]) && runes[j] != '\n' {
				j++
			}
			delim, n := readHeredocDelim(runes[j:])
			j += n
			for j < len(runes) && runes[j] != '\n' {
				j++
			}
			if j < len(runes) {
				j++
			}
			bodyStart := byteAt(j)
			for j < len(runes) {
				start := j
				for j < len(runes) && runes[j] != '\n' {
					j++
				}
				line := string(runes[start:j])
				check := line
				if dash {
					check = strings.TrimLeft(line, "\t")
				}
				if check == delim {
					ranges = append(ranges, byteRange{bodyStart, byteAt(start)})
					break
				}
				if j < len(runes) {
					j++
				}
			}
			i = j
			continue
		}
		i++
	}
	return ranges
}

func rangeFullyInside(start, end int, ranges []byteRange) bool {
	if start >= end {
		return false
	}
	for _, r := range ranges {
		if start >= r.start && end <= r.end {
			return true
		}
	}
	return false
}

// highlightBashCommand paints keywords, strings, comments, operators, and
// heredoc payload. Highlighted spans flatten back to the input (modulo ANSI).
func highlightBashCommand(command string) string {
	if command == "" {
		return ""
	}
	payload := heredocPayloadRanges(command)
	var b strings.Builder
	i := 0
	inSingle, inDouble := false, false
	for i < len(command) {
		if rangeFullyInside(i, i+1, payload) {
			start := i
			for i < len(command) && rangeFullyInside(i, i+1, payload) {
				i++
			}
			b.WriteString(bashHeredocStyle.Render(command[start:i]))
			continue
		}
		c := command[i]
		if !inSingle && !inDouble && c == '#' {
			end := strings.IndexByte(command[i:], '\n')
			if end < 0 {
				b.WriteString(bashCommentStyle.Render(command[i:]))
				break
			}
			b.WriteString(bashCommentStyle.Render(command[i : i+end]))
			i += end
			continue
		}
		if !inDouble && c == '\'' {
			end := i + 1
			for end < len(command) && command[end] != '\'' {
				end++
			}
			if end < len(command) {
				end++
			}
			b.WriteString(bashStrStyle.Render(command[i:end]))
			i = end
			continue
		}
		if !inSingle && c == '"' {
			end := i + 1
			for end < len(command) {
				if command[end] == '\\' {
					end += 2
					continue
				}
				if command[end] == '"' {
					end++
					break
				}
				end++
			}
			b.WriteString(bashStrStyle.Render(command[i:end]))
			i = end
			continue
		}
		if !inSingle && !inDouble {
			if tok, n := matchBashOp(command[i:]); n > 0 {
				b.WriteString(bashOpStyle.Render(tok))
				i += n
				continue
			}
			if isWordStart(command[i]) {
				end := i + 1
				for end < len(command) && isWordChar(command[end]) {
					end++
				}
				word := command[i:end]
				if _, ok := bashKeywords[word]; ok {
					b.WriteString(bashKwStyle.Render(word))
				} else {
					b.WriteString(codeStyle.Render(word))
				}
				i = end
				continue
			}
		}
		if c == '\'' {
			inSingle = !inSingle
		} else if c == '"' {
			inDouble = !inDouble
		}
		b.WriteString(codeStyle.Render(command[i : i+1]))
		i++
	}
	return b.String()
}

func matchBashOp(s string) (string, int) {
	ops := []string{"&&", "||", ">>", "<<", ">&", "<&", "|&", ";;", ";"}
	for _, op := range ops {
		if strings.HasPrefix(s, op) {
			return op, len(op)
		}
	}
	if s != "" && strings.ContainsRune("|><&", rune(s[0])) {
		return s[:1], 1
	}
	return "", 0
}

func isWordStart(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isWordChar(b byte) bool {
	return isWordStart(b) || (b >= '0' && b <= '9') || b == '-'
}

func truncationIndicator() string {
	return dimStyle.Render("... ") + toolStyle.Render("Ctrl-F") + dimStyle.Render(" to expand")
}
