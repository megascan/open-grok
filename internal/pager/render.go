package pager

import (
	"strings"
	"unicode/utf8"
)

// renderMarkdown applies a small, dependency-free transcript highlighter.
func renderMarkdown(src string, width int) string {
	return renderMarkdownEx(src, width, true)
}

func renderMarkdownEx(src string, width int, mermaid bool) string {
	if src == "" {
		return ""
	}
	if width < 20 {
		width = 20
	}
	lines := strings.Split(src, "\n")
	var out []string
	inFence := false
	fenceKind := ""
	var fence []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			if inFence {
				inFence = false
				body := strings.Join(fence, "\n")
				if fenceKind == "mermaid" {
					if mermaid {
						out = append(out, renderMermaidASCII(body, width))
					} else {
						out = append(out, mermaidFallbackCard(body, width))
					}
				} else {
					painted := highlightByLang(fenceKind, fence)
					for i, fl := range fence {
						line := codeStyle.Render(fl)
						if i < len(painted) && painted[i] != fl {
							line = painted[i]
						}
						out = append(out, wrap(line, width)...)
					}
					out = append(out, dimStyle.Render(trim))
				}
				fence = nil
				fenceKind = ""
				continue
			}
			inFence = true
			info := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
			fenceKind = strings.ToLower(info)
			if i := strings.IndexByte(fenceKind, ' '); i >= 0 {
				fenceKind = fenceKind[:i]
			}
			if fenceKind != "mermaid" {
				out = append(out, dimStyle.Render(trim))
			}
			continue
		}
		if inFence {
			fence = append(fence, line)
			continue
		}
		switch {
		case strings.HasPrefix(trim, "### "):
			out = append(out, wrap(headMd.Render(trim[4:]), width)...)
		case strings.HasPrefix(trim, "## "):
			out = append(out, wrap(headMd.Render(trim[3:]), width)...)
		case strings.HasPrefix(trim, "# "):
			out = append(out, wrap(headMd.Render(trim[2:]), width)...)
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* "):
			out = append(out, wrap(bulletMd.Render("• ")+inlineMD(trim[2:]), width)...)
		default:
			out = append(out, wrap(inlineMD(line), width)...)
		}
	}
	if inFence && fenceKind == "mermaid" {
		body := strings.Join(fence, "\n")
		if mermaid {
			out = append(out, renderMermaidASCII(body, width))
		} else {
			out = append(out, mermaidFallbackCard(body, width))
		}
	}
	return strings.Join(out, "\n")
}

func inlineMD(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "**") {
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				b.WriteString(lipglossBold(s[i+2 : i+2+j]))
				i += 4 + j
				continue
			}
		}
		if s[i] == '`' {
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				b.WriteString(codeStyle.Render(s[i+1 : i+1+j]))
				i += 2 + j
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func lipglossBold(s string) string {
	return agentStyle.Render(s)
}

func wrap(s string, width int) []string {
	plain := stripANSI(s)
	if width <= 0 || runeLen(plain) <= width {
		return []string{s}
	}
	// ANSI-unaware wrap is acceptable for the transcript; lipgloss width
	// helpers would re-wrap styled strings poorly mid-escape.
	var lines []string
	for _, part := range strings.Split(s, "\n") {
		rest := part
		for runeLen(stripANSI(rest)) > width {
			cut := cutANSI(rest, width)
			if cut == "" || cut == rest {
				lines = append(lines, rest)
				rest = ""
				break
			}
			lines = append(lines, cut)
			rest = strings.TrimPrefix(rest, cut)
		}
		if rest != "" {
			lines = append(lines, rest)
		}
	}
	return lines
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				if (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z') {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func cutANSI(s string, width int) string {
	var b strings.Builder
	n := 0
	i := 0
	for i < len(s) && n < width {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				if (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z') {
					j++
					break
				}
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
		n++
	}
	return b.String()
}
