package pager

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type mermaidNode struct {
	ID, Label, Shape string
}

type mermaidEdge struct {
	From, To, Label string
}

var edgeRe = regexp.MustCompile(`^([A-Za-z0-9_]+)(?:\[[^\]]*\]|\([^\)]*\)|\{[^}]*\}|\(\([^)]*\)\))?\s*(-->|---|-.->|==>)\s*(?:\|([^|]+)\|)?\s*([A-Za-z0-9_]+)(?:\[[^\]]*\]|\(([^\)]*)\)|\{([^}]*)\}|\(\(([^)]*)\)\))?`)

func renderMermaidASCII(src string, width int) string {
	return renderMermaidASCIIEx(src, width, false)
}

func renderMermaidASCIIEx(src string, width int, rendering bool) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return mermaidCard("(empty diagram)", width, rendering)
	}
	kind, dir, nodes, edges, extras := parseMermaid(src)
	switch kind {
	case "sequencediagram":
		return mermaidCard(renderSequenceASCII(extras, width), width, rendering)
	case "pie":
		return mermaidCard(renderPieASCII(extras, width), width, rendering)
	case "flowchart", "graph":
		return mermaidCard(renderFlowchartASCII(dir, nodes, edges, width), width, rendering)
	default:
		body := src
		if len(body) > 800 {
			body = body[:800] + "…"
		}
		return mermaidCard(body, width, rendering)
	}
}

func mermaidCard(body string, width int, rendering bool) string {
	inner := codeStyle.Render(body) + "\n" + mermaidAffordanceLine(rendering)
	return toolCard.Width(max(20, width-2)).Render(inner)
}

func mermaidFallbackCard(src string, width int) string {
	return toolCard.Width(max(20, width-2)).Render(toolStyle.Render("mermaid") + "\n" + dimStyle.Render("diagram source (render_mermaid off)") + "\n" + codeStyle.Render(truncate(src, 400)))
}

func parseMermaid(src string) (kind, dir string, nodes []mermaidNode, edges []mermaidEdge, extras []string) {
	dir = "TD"
	kind = "flowchart"
	seen := map[string]int{}
	addNode := func(id, label, shape string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if label == "" {
			label = id
		}
		if i, ok := seen[id]; ok {
			if label != id && nodes[i].Label == nodes[i].ID {
				nodes[i].Label = label
			}
			return
		}
		seen[id] = len(nodes)
		nodes = append(nodes, mermaidNode{ID: id, Label: label, Shape: shape})
	}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "sequencediagram"):
			kind = "sequencediagram"
			continue
		case strings.HasPrefix(low, "pie"):
			kind = "pie"
			continue
		case strings.HasPrefix(low, "flowchart") || strings.HasPrefix(low, "graph"):
			kind = "flowchart"
			fields := strings.Fields(line)
			if len(fields) > 1 {
				dir = strings.ToUpper(fields[1])
			}
			continue
		}
		if kind == "sequencediagram" || kind == "pie" {
			extras = append(extras, line)
			continue
		}
		if m := edgeRe.FindStringSubmatch(line); m != nil {
			addNode(m[1], extractInlineLabel(line, m[1]), "rect")
			addNode(m[4], firstNonEmpty(m[5], m[6], m[7], m[4]), "rect")
			if lab := bracketLabel(line, m[1]); lab != "" {
				addNode(m[1], lab, "rect")
			}
			if lab := bracketLabelAfter(line, m[4]); lab != "" {
				addNode(m[4], lab, "rect")
			}
			edges = append(edges, mermaidEdge{From: m[1], To: m[4], Label: strings.TrimSpace(m[3])})
			continue
		}
		// A[Label] or A{Label} standalone
		if n := parseStandaloneNode(line); n.ID != "" {
			addNode(n.ID, n.Label, n.Shape)
			continue
		}
		extras = append(extras, line)
	}
	return kind, dir, nodes, edges, extras
}

func parseStandaloneNode(line string) mermaidNode {
	line = strings.TrimSpace(line)
	for _, wrap := range []struct{ open, close, shape string }{
		{"[", "]", "rect"},
		{"((", "))", "circle"},
		{"{", "}", "diamond"},
		{"(", ")", "round"},
	} {
		open := strings.Index(line, wrap.open)
		if open <= 0 {
			continue
		}
		close := strings.LastIndex(line, wrap.close)
		if close <= open {
			continue
		}
		id := strings.TrimSpace(line[:open])
		if !isIdent(id) {
			continue
		}
		return mermaidNode{ID: id, Label: strings.TrimSpace(line[open+len(wrap.open) : close]), Shape: wrap.shape}
	}
	if isIdent(line) {
		return mermaidNode{ID: line, Label: line, Shape: "rect"}
	}
	return mermaidNode{}
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func bracketLabel(line, id string) string {
	idx := strings.Index(line, id)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(id):]
	return parseStandaloneNode(id + rest).Label
}

func bracketLabelAfter(line, id string) string {
	idx := strings.LastIndex(line, id)
	if idx < 0 {
		return ""
	}
	return parseStandaloneNode(line[idx:]).Label
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func extractInlineLabel(line, id string) string {
	if lab := bracketLabel(line, id); lab != "" && lab != id {
		return lab
	}
	return id
}

func renderFlowchartASCII(dir string, nodes []mermaidNode, edges []mermaidEdge, width int) string {
	if len(nodes) == 0 {
		return "(no nodes)"
	}
	incoming := map[string]int{}
	out := map[string][]mermaidEdge{}
	for _, e := range edges {
		incoming[e.To]++
		out[e.From] = append(out[e.From], e)
	}
	var roots []string
	for _, n := range nodes {
		if incoming[n.ID] == 0 {
			roots = append(roots, n.ID)
		}
	}
	if len(roots) == 0 {
		roots = []string{nodes[0].ID}
	}
	byID := map[string]mermaidNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	generations := [][]string{}
	seen := map[string]bool{}
	cur := roots
	for len(cur) > 0 && len(generations) < 16 {
		gen := []string{}
		var next []string
		for _, id := range cur {
			if seen[id] {
				continue
			}
			seen[id] = true
			gen = append(gen, id)
			for _, e := range out[id] {
				if !seen[e.To] {
					next = append(next, e.To)
				}
			}
		}
		if len(gen) > 0 {
			generations = append(generations, gen)
		}
		cur = next
	}
	for _, n := range nodes {
		if !seen[n.ID] {
			generations = append(generations, []string{n.ID})
		}
	}
	boxW := 16
	if width > 60 {
		boxW = 22
	}
	var b strings.Builder
	lr := dir == "LR" || dir == "RL"
	for gi, gen := range generations {
		if lr && gi > 0 {
			b.WriteString("  →  ")
		}
		var boxes []string
		for _, id := range gen {
			boxes = append(boxes, drawNodeBox(byID[id], boxW))
		}
		if lr {
			b.WriteString(joinHorizontal(boxes, "  "))
		} else {
			b.WriteString(joinHorizontal(boxes, "   "))
			b.WriteByte('\n')
			if gi+1 < len(generations) {
				labels := edgeLabelsBetween(gen, generations[gi+1], edges)
				arrow := "        ▼"
				if labels != "" {
					arrow = "        ▼ " + labels
				}
				b.WriteString(dimStyle.Render(arrow) + "\n")
			}
		}
	}
	if lr {
		b.WriteByte('\n')
	}
	if len(edges) > 0 {
		b.WriteString(dimStyle.Render("edges:") + "\n")
		for _, e := range edges {
			lab := e.From + " → " + e.To
			if e.Label != "" {
				lab += "  (" + e.Label + ")"
			}
			from := byID[e.From].Label
			to := byID[e.To].Label
			if from != "" && to != "" {
				lab = from + " → " + to
				if e.Label != "" {
					lab += "  (" + e.Label + ")"
				}
			}
			b.WriteString("  " + lab + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func edgeLabelsBetween(fromIDs, toIDs []string, edges []mermaidEdge) string {
	wantFrom := map[string]bool{}
	wantTo := map[string]bool{}
	for _, id := range fromIDs {
		wantFrom[id] = true
	}
	for _, id := range toIDs {
		wantTo[id] = true
	}
	var labs []string
	for _, e := range edges {
		if wantFrom[e.From] && wantTo[e.To] && e.Label != "" {
			labs = append(labs, e.Label)
		}
	}
	return strings.Join(labs, ", ")
}

func drawNodeBox(n mermaidNode, w int) string {
	label := n.Label
	if label == "" {
		label = n.ID
	}
	label = truncate(label, w-2)
	pad := w - 2 - len([]rune(label))
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	top, bot := "┌"+strings.Repeat("─", w-2)+"┐", "└"+strings.Repeat("─", w-2)+"┘"
	if n.Shape == "diamond" {
		top, bot = "╱"+strings.Repeat("─", w-2)+"╲", "╲"+strings.Repeat("─", w-2)+"╱"
	}
	if n.Shape == "round" || n.Shape == "circle" {
		top, bot = "╭"+strings.Repeat("─", w-2)+"╮", "╰"+strings.Repeat("─", w-2)+"╯"
	}
	mid := "│" + strings.Repeat(" ", left) + label + strings.Repeat(" ", right) + "│"
	return top + "\n" + mid + "\n" + bot
}

func joinHorizontal(blocks []string, gap string) string {
	if len(blocks) == 0 {
		return ""
	}
	var split [][]string
	height := 0
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		split = append(split, lines)
		if len(lines) > height {
			height = len(lines)
		}
	}
	var rows []string
	for r := 0; r < height; r++ {
		var row []string
		for _, lines := range split {
			cell := ""
			if r < len(lines) {
				cell = lines[r]
			}
			row = append(row, cell)
		}
		rows = append(rows, strings.Join(row, gap))
	}
	return strings.Join(rows, "\n")
}

func renderSequenceASCII(lines []string, width int) string {
	var parts []string
	seen := map[string]bool{}
	type msg struct{ From, To, Text string }
	var msgs []msg
	for _, line := range lines {
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "participant ") || strings.HasPrefix(low, "actor ") {
			name := strings.TrimSpace(line[strings.IndexByte(line, ' ')+1:])
			name = strings.Trim(name, `"'`)
			if i := strings.Index(strings.ToLower(name), " as "); i >= 0 {
				name = strings.TrimSpace(name[i+4:])
			}
			if name != "" && !seen[name] {
				seen[name] = true
				parts = append(parts, name)
			}
			continue
		}
		arrow := "->>"
		if strings.Contains(line, "-->>") {
			arrow = "-->>"
		} else if strings.Contains(line, "->>") {
			arrow = "->>"
		} else if strings.Contains(line, "->") {
			arrow = "->"
		} else {
			continue
		}
		idx := strings.Index(line, arrow)
		left := strings.TrimSpace(line[:idx])
		rest := strings.TrimSpace(line[idx+len(arrow):])
		to, text, _ := strings.Cut(rest, ":")
		to = strings.TrimSpace(to)
		text = strings.TrimSpace(text)
		if left != "" && !seen[left] {
			seen[left] = true
			parts = append(parts, left)
		}
		if to != "" && !seen[to] {
			seen[to] = true
			parts = append(parts, to)
		}
		msgs = append(msgs, msg{From: left, To: to, Text: text})
	}
	if len(parts) == 0 {
		return strings.Join(lines, "\n")
	}
	colW := max(8, min(18, (width-2)/max(1, len(parts))))
	pad := func(s string) string {
		s = truncate(s, colW-1)
		return s + strings.Repeat(" ", colW-len([]rune(s)))
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(pad(p))
	}
	b.WriteByte('\n')
	for range parts {
		b.WriteString(pad("│"))
	}
	b.WriteByte('\n')
	for _, m := range msgs {
		fi, ti := indexOf(parts, m.From), indexOf(parts, m.To)
		if fi < 0 || ti < 0 {
			continue
		}
		row := make([]string, len(parts))
		for i := range row {
			row[i] = pad("│")
		}
		label := truncate(m.Text, colW-2)
		if fi == ti {
			row[fi] = pad("│ " + label)
		} else if fi < ti {
			row[fi] = pad("├" + strings.Repeat("─", max(1, colW-2)))
			for i := fi + 1; i < ti; i++ {
				row[i] = strings.Repeat("─", colW)
			}
			row[ti] = pad("► " + label)
		} else {
			row[ti] = pad("◄ " + label)
			for i := ti + 1; i < fi; i++ {
				row[i] = strings.Repeat("─", colW)
			}
			row[fi] = pad(strings.Repeat("─", max(1, colW-1)) + "┤")
		}
		b.WriteString(strings.Join(row, ""))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func renderPieASCII(lines []string, width int) string {
	type slice struct {
		Label string
		Value float64
	}
	var slices []slice
	total := 0.0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "title ") {
			continue
		}
		label, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		label = strings.Trim(strings.TrimSpace(label), `"`)
		var v float64
		fmt.Sscanf(strings.TrimSpace(rest), "%f", &v)
		slices = append(slices, slice{label, v})
		total += v
	}
	if total == 0 {
		return strings.Join(lines, "\n")
	}
	barW := max(10, min(28, width-18))
	var b strings.Builder
	for _, s := range slices {
		pct := s.Value / total
		fill := int(pct * float64(barW))
		bar := strings.Repeat("█", fill) + strings.Repeat("░", barW-fill)
		b.WriteString(fmt.Sprintf("%-10s %s  %2.0f%%\n", truncate(s.Label, 10), bar, pct*100))
	}
	return strings.TrimRight(b.String(), "\n")
}
