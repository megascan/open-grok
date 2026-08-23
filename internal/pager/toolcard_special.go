package pager

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/megascan/open-grok/internal/workspace"
)

const (
	maxInlineSources = 3
	mcpTruncInline   = 10
)

var httpURLRe = regexp.MustCompile(`https?://[^\s)\]"'<>]+`)

func webSearchLabel(title string) string {
	name := toolName(title)
	if name == "x_search" || strings.Contains(name, "x_search") {
		return "X Search "
	}
	return "Web Search "
}

func webSearchQuery(args map[string]string, body string) string {
	q := toolArgValue(args, "query", "q", "search")
	if q == "" {
		q = firstLine(body)
	}
	if strings.HasPrefix(q, "{") {
		return ""
	}
	return q
}

func webSearchHeader(title string, args map[string]string, body, result string, collapsed bool) string {
	prefix := webSearchLabel(title)
	q := webSearchQuery(args, body)
	header := strings.TrimSpace(prefix)
	if q != "" {
		header = prefix + q
	}
	if !collapsed {
		return header
	}
	n := len(uniqueDomains(extractURLs(result)))
	if n == 1 {
		return header + " (1 site)"
	}
	if n > 1 {
		return header + " (" + strconv.Itoa(n) + " sites)"
	}
	return header
}

func extractURLs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return httpURLRe.FindAllString(s, -1)
}

func uniqueDomains(urls []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

func webSearchSourcesLine(result string) string {
	unique := uniqueDomains(extractURLs(result))
	if len(unique) == 0 {
		return ""
	}
	shown := unique
	more := 0
	if len(unique) > maxInlineSources {
		shown = unique[:maxInlineSources]
		more = len(unique) - maxInlineSources
	}
	line := "  Sources: " + strings.Join(shown, ", ")
	if more > 0 {
		line += " (+" + strconv.Itoa(more) + " more)"
	}
	return line
}

func renderWebSearchCard(title string, args map[string]string, body, result string, failed, folded bool) string {
	header := webSearchHeader(title, args, body, result, folded)
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	var parts []string
	if src := webSearchSourcesLine(result); src != "" {
		parts = append(parts, dimStyle.Render(src))
	}
	name := toolName(title)
	if name != "x_search" && !strings.Contains(name, "x_search") {
		if preview := indentTruncated(result, fetchMaxInline); preview != "" {
			parts = append(parts, preview)
		} else if src := webSearchSourcesLine(result); src == "" {
			parts = append(parts, dimStyle.Render("  (no content)"))
		}
	}
	return renderAccentBlockRaw(toolStyle.Render(header), strings.Join(parts, "\n"), false)
}

func indentTruncated(result string, max int) string {
	s := strings.TrimRight(result, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > max {
		shown := make([]string, 0, max+1)
		for i := 0; i < max; i++ {
			shown = append(shown, "  "+lines[i])
		}
		shown = append(shown, dimStyle.Render(fmt.Sprintf("  ... (%d more lines, press Enter to view)", len(lines)-max)))
		return strings.Join(shown, "\n")
	}
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "  " + line
	}
	return strings.Join(indented, "\n")
}

func memorySearchHeader(args map[string]string, body, result string) string {
	q := toolArgValue(args, "query", "q")
	if q == "" {
		q = firstLine(body)
	}
	header := "Memory Search"
	if q != "" && !strings.HasPrefix(q, "{") {
		header += " " + q
	}
	n := memoryResultCount(result)
	if n == 1 {
		header += " (1 result)"
	} else if n > 1 {
		header += " (" + strconv.Itoa(n) + " results)"
	}
	return header
}

func memoryResultCount(result string) int {
	s := strings.TrimSpace(result)
	if s == "" || s == "no matching memory" || s == "no memory notes" {
		return 0
	}
	n := 0
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func renderMemorySearchCard(header, result string, failed, folded bool) string {
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	body := renderMemoryBody(result)
	return renderAccentBlockRaw(toolStyle.Render(header), body, false)
}

func renderMemoryBody(result string) string {
	s := strings.TrimSpace(result)
	if s == "" || s == "no matching memory" || s == "no memory notes" {
		return dimStyle.Render("  (no results)")
	}
	type hit struct {
		name    string
		snippet []string
	}
	var hits []hit
	var cur hit
	flush := func() {
		if cur.name != "" {
			hits = append(hits, cur)
		}
		cur = hit{}
	}
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			flush()
			cur.name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "## "))
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if cur.name == "" {
			cur.name = "memory"
		}
		if len(cur.snippet) < 3 {
			cur.snippet = append(cur.snippet, strings.TrimSpace(line))
		}
	}
	flush()
	if len(hits) == 0 {
		return indentTruncated(result, fetchMaxInline)
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d. ", i+1)))
		b.WriteString(h.name)
		for _, sl := range h.snippet {
			b.WriteByte('\n')
			b.WriteString(dimStyle.Render("    " + sl))
		}
	}
	return b.String()
}

func searchToolsHeader(args map[string]string, body, result string) string {
	q := toolArgValue(args, "query", "q", "pattern")
	if q == "" {
		q = firstLine(body)
	}
	header := "Search Tools"
	if q != "" && !strings.HasPrefix(q, "{") {
		header += " " + q
	}
	n := countResultLines(result)
	if strings.TrimSpace(result) == "" {
		return header
	}
	if n == 1 {
		header += " (1 result)"
	} else {
		header += " (" + strconv.Itoa(n) + " results)"
	}
	return header
}

func renderSearchToolsCard(header, result string, failed, folded bool) string {
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	if strings.TrimSpace(result) == "" {
		return renderAccentBlockRaw(toolStyle.Render(header), dimStyle.Render("  (no results)"), false)
	}
	return renderAccentBlockRaw(toolStyle.Render(header), indentTruncated(result, fetchMaxInline), false)
}

func mcpToolHeader(title string, args map[string]string) string {
	raw := strings.TrimLeft(strings.TrimSpace(title), "▸ ")
	if v := toolArgValue(args, "tool", "tool_name", "name"); v != "" {
		raw = v
	}
	if raw == "" || raw == "use_tool" {
		raw = toolName(title)
	}
	server, action, ok := splitMCPName(raw)
	if !ok {
		return workspace.MCPTitleizeSegment(raw)
	}
	return workspace.MCPTitleizeSegment(server) + " " + workspace.MCPTitleizeSegment(action)
}

func splitMCPName(name string) (server, action string, ok bool) {
	if i := strings.Index(name, workspace.MCPToolNameDelimiter); i > 0 {
		return name[:i], name[i+len(workspace.MCPToolNameDelimiter):], true
	}
	if strings.HasPrefix(name, "mcp_") {
		rest := strings.TrimPrefix(name, "mcp_")
		if i := strings.IndexByte(rest, '_'); i > 0 {
			return rest[:i], rest[i+1:], true
		}
	}
	return "", name, false
}

func renderMCPCard(title string, args map[string]string, body, result string, failed, folded bool) string {
	header := mcpToolHeader(title, args)
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	var parts []string
	if len(args) > 0 {
		keys := make([]string, 0, len(args))
		for k := range args {
			if k == "tool" || k == "tool_name" || k == "name" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, dimStyle.Render("  "+k+": ")+args[k])
		}
	}
	if failed {
		if strings.TrimSpace(result) != "" {
			parts = append(parts, strings.TrimRight(result, "\n"))
		}
		return renderAccentBlockRaw(errStyle.Render(header), strings.Join(parts, "\n"), true)
	}
	if preview := indentTruncated(result, mcpTruncInline); preview != "" {
		parts = append(parts, preview)
	} else if len(parts) == 0 {
		parts = append(parts, dimStyle.Render("  (no output)"))
	}
	_ = body
	return renderAccentBlockRaw(toolStyle.Render(header), strings.Join(parts, "\n"), false)
}

func otherToolHeader(title string, args map[string]string, body string) string {
	name := strings.TrimLeft(strings.TrimSpace(title), "▸ ")
	if name == "" {
		name = "tool"
	}
	label, rest, ok := strings.Cut(name, ": ")
	header := name
	if ok {
		header = strings.TrimSpace(label) + " " + strings.TrimSpace(rest)
	}
	summary := toolArgValue(args, "description", "summary", "target")
	if summary == "" && parseToolArgs(body) == nil {
		summary = firstLine(body)
		if summary == name {
			summary = ""
		}
	}
	if summary != "" {
		header += "  " + summary
	}
	return header
}

func renderOtherCard(header string, args map[string]string, body, result string, failed, folded bool) string {
	if folded {
		return renderAccentBlock(header, "", failed)
	}
	if failed {
		return renderAccentBlock(header, strings.TrimRight(result, "\n"), true)
	}
	_ = args
	_ = body
	if strings.TrimSpace(result) == "" {
		return renderAccentBlock(header, "", false)
	}
	return renderAccentBlock(header, truncateResultBody(result), false)
}
