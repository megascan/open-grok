package pager

import (
	"fmt"
	"strconv"
	"strings"
)

func renderToolCard(title, argsBody, result string, failed, folded bool) string {
	kind := classifyTool(title)
	args := parseToolArgs(argsBody)
	header := toolCallHeaderFull(title, argsBody, result, folded)
	switch kind {
	case verbEdit:
		return renderEditCard(title, args, argsBody, result, failed, folded)
	case verbFile:
		return renderReadCard(header, args, result, failed, folded)
	case verbSearch, verbWebSearch, verbMemory, verbMCPSearch:
		return renderSearchCard(header, result, failed, folded)
	case verbDir:
		return renderListCard(header, result, failed, folded)
	case verbWebFetch:
		return renderFetchCard(header, result, failed, folded)
	case verbCommand:
		return renderRunCard(args, argsBody, result, failed, folded)
	default:
		if folded {
			return renderAccentBlock(header, "", failed)
		}
		return renderAccentBlock(header, truncateResultBody(result), failed)
	}
}

func renderEditCard(title string, args map[string]string, body, result string, failed, folded bool) string {
	card := parseEditCard(title, args, body)
	head := styledEditHeader(card, folded)
	if failed && strings.TrimSpace(result) != "" && folded {
		return renderAccentBlockRaw(head, "", true)
	}
	if folded {
		return renderAccentBlockRaw(head, "", failed)
	}
	var bodyText string
	if failed && strings.TrimSpace(result) != "" && len(card.Hunks) == 0 {
		bodyText = strings.TrimRight(result, "\n")
	} else if len(card.Hunks) > 0 {
		bodyText = renderDiffHunks(card.Hunks)
	} else {
		bodyText = truncateResultBody(result)
	}
	return renderAccentBlockRaw(head, bodyText, failed)
}

func styledEditHeader(card editCard, collapsed bool) string {
	prefix := card.Prefix
	if strings.TrimSpace(prefix) == "" {
		prefix = "Edit "
	}
	path := card.Path
	head := toolStyle.Render(prefix)
	if path != "" {
		head += toolStyle.Render(path)
	}
	if !collapsed || card.Untrusted {
		return head
	}
	ins, del := countInsDelHunks(card.Hunks)
	if ins > 0 || del > 0 {
		return head + insertStyle.Render(" +"+strconv.Itoa(ins)) + dimStyle.Render("/") + deleteStyle.Render("-"+strconv.Itoa(del))
	}
	if card.EditCount > 1 {
		return head + dimStyle.Render(" ("+strconv.Itoa(card.EditCount)+" edits)")
	}
	return head
}

func renderReadCard(header string, args map[string]string, result string, failed, folded bool) string {
	if folded || strings.TrimSpace(result) == "" {
		return renderAccentBlock(header, "", failed)
	}
	start, _ := strconv.Atoi(toolArgValue(args, "offset", "start_line", "start"))
	if start <= 0 {
		start = 1
	}
	lines, nums := parseReadContent(result, start)
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	shown := truncateIndexedLines(lines, nums, readFirstLines, readLastLines, false)
	return renderAccentBlockRaw(toolStyle.Render(header), shown, false)
}

func parseReadContent(result string, fallbackStart int) (lines []string, nums []int) {
	raw := strings.Split(strings.TrimRight(result, "\n"), "\n")
	numbered := true
	for i, line := range raw {
		n, ok := parseReadGutter(line)
		if !ok {
			numbered = false
			break
		}
		bar := strings.IndexByte(line, '|')
		lines = append(lines, line[bar+1:])
		nums = append(nums, n)
		_ = i
	}
	if numbered && len(lines) > 0 {
		return lines, nums
	}
	lines = raw
	nums = make([]int, len(lines))
	for i := range lines {
		nums[i] = fallbackStart + i
	}
	return lines, nums
}

func truncateIndexedLines(lines []string, nums []int, first, last int, plusHidden bool) string {
	if len(lines) == 0 {
		return ""
	}
	w := 1
	for _, n := range nums {
		d := 1
		for x := n; x >= 10; x /= 10 {
			d++
		}
		if d > w {
			w = d
		}
	}
	format := func(i int) string {
		gutter := fmt.Sprintf("%*d  ", w, nums[i])
		return dimStyle.Render(gutter) + lines[i]
	}
	if len(lines) <= first+last+1 {
		var b strings.Builder
		for i := range lines {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(format(i))
		}
		return b.String()
	}
	var b strings.Builder
	for i := 0; i < first; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(format(i))
	}
	hidden := len(lines) - first - last
	b.WriteByte('\n')
	if plusHidden {
		b.WriteString(dimStyle.Render("… +" + strconv.Itoa(hidden) + " lines"))
	} else {
		b.WriteString(dimStyle.Render("…"))
	}
	for i := len(lines) - last; i < len(lines); i++ {
		b.WriteByte('\n')
		b.WriteString(format(i))
	}
	return b.String()
}

func renderSearchCard(header, result string, failed, folded bool) string {
	if folded || strings.TrimSpace(result) == "" {
		return renderAccentBlock(header, "", failed)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	return renderAccentBlockRaw(toolStyle.Render(header), renderSearchBody(result), false)
}

func renderSearchBody(result string) string {
	s := strings.TrimSpace(result)
	if s == "" || s == "no matches" || s == "no files matched" {
		return dimStyle.Render("  (no results)")
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	looksGrep := false
	for _, line := range lines {
		if strings.Count(line, ":") >= 2 {
			looksGrep = true
			break
		}
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render("  mode: ") + "pattern")
	if !looksGrep {
		b.WriteByte('\n')
		for i, line := range lines {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  " + line)
		}
		return b.String()
	}
	type hit struct {
		path string
		num  string
		text string
	}
	var hits []hit
	for _, line := range lines {
		path, rest, ok := strings.Cut(line, ":")
		if !ok {
			hits = append(hits, hit{path: line})
			continue
		}
		num, text, ok := strings.Cut(rest, ":")
		if !ok {
			hits = append(hits, hit{path: path, text: rest})
			continue
		}
		hits = append(hits, hit{path: path, num: num, text: text})
	}
	prev := ""
	for _, h := range hits {
		if h.path != prev {
			if prev != "" {
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
			b.WriteString(toolStyle.Render("  " + h.path))
			prev = h.path
		}
		if h.num != "" || h.text != "" {
			b.WriteByte('\n')
			b.WriteString(dimStyle.Render(fmt.Sprintf("    %4s  ", h.num)) + h.text)
		}
	}
	return b.String()
}

func renderListCard(header, result string, failed, folded bool) string {
	if folded || strings.TrimSpace(result) == "" {
		return renderAccentBlock(header, "", failed)
	}
	var b strings.Builder
	for i, line := range strings.Split(strings.TrimRight(result, "\n"), "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  " + line)
	}
	return renderAccentBlockRaw(toolStyle.Render(header), b.String(), failed)
}

func renderFetchCard(header, result string, failed, folded bool) string {
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	if strings.TrimSpace(result) == "" && !failed {
		return renderAccentBlockRaw(toolStyle.Render(header), dimStyle.Render("  (no content)"), false)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	max := fetchMaxInline
	if len(lines) > max {
		shown := make([]string, 0, max+1)
		for i := 0; i < max; i++ {
			shown = append(shown, "  "+lines[i])
		}
		shown = append(shown, dimStyle.Render(fmt.Sprintf("  ... (%d more lines, press Enter to view)", len(lines)-max)))
		return renderAccentBlockRaw(toolStyle.Render(header), strings.Join(shown, "\n"), false)
	}
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "  " + line
	}
	return renderAccentBlockRaw(toolStyle.Render(header), strings.Join(indented, "\n"), false)
}

func renderRunCard(args map[string]string, body, result string, failed, folded bool) string {
	cmd := commandFromArgs(args, body)
	var head strings.Builder
	if cmd == "" {
		head.WriteString(toolStyle.Render("Run"))
	} else {
		head.WriteString(toolStyle.Render("Run ") + highlightBashCommand(cmd))
	}
	if folded || strings.TrimSpace(result) == "" {
		return renderAccentBlockRaw(head.String(), "", failed)
	}
	return renderAccentBlockRaw(head.String(), truncateExecuteBody(result), failed)
}

func truncateExecuteBody(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= toolResultFirstLines+toolResultLastLines {
		return s
	}
	hidden := len(lines) - toolResultFirstLines - toolResultLastLines
	var b strings.Builder
	b.WriteString(strings.Join(lines[:toolResultFirstLines], "\n"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("… +" + strconv.Itoa(hidden) + " lines"))
	b.WriteString("\n")
	b.WriteString(strings.Join(lines[len(lines)-toolResultLastLines:], "\n"))
	return b.String()
}

func renderAccentBlockRaw(header, body string, failed bool) string {
	var b strings.Builder
	if failed {
		writeAccent(&b, errStyle.Render(stripANSI(header)))
	} else {
		writeAccent(&b, header)
	}
	if strings.TrimSpace(stripANSI(body)) == "" {
		return strings.TrimRight(b.String(), "\n")
	}
	for _, line := range strings.Split(body, "\n") {
		if failed {
			writeAccent(&b, errStyle.Render(stripANSI(line)))
		} else {
			writeAccent(&b, line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
