package pager

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MAX_VISIBLE_SUGGESTIONS matches crates/.../slash/mod.rs.
const maxVisibleSuggestions = 6

const (
	slashLabelCap    = 40
	slashLabelGap    = 2
	slashPromptArrow = "\u276F " // ❯ plus space; always 2 columns
)

// renderSlashDropdown paints the inline `/` completion list above the prompt,
// matching views/slash_dropdown.rs chrome (❯ prefix, aligned labels, max 6 rows).
func (m *model) renderSlashDropdown() string {
	if m.overlay != overlayPalette {
		return ""
	}
	val := strings.TrimSpace(m.ta.Value())
	if !strings.HasPrefix(val, "/") {
		return ""
	}
	hits := FuzzySlash(m.filter, m.screenMd)
	if len(hits) == 0 {
		return dimStyle.Render("  (no matches)")
	}
	if m.cursor >= len(hits) {
		m.cursor = len(hits) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	w := m.transcriptWidth()
	if w < 8 {
		w = 8
	}
	labelW := slashLabelColumnWidth(hits, w)
	start := 0
	if m.cursor >= maxVisibleSuggestions {
		start = m.cursor - maxVisibleSuggestions + 1
	}
	end := start + maxVisibleSuggestions
	if end > len(hits) {
		end = len(hits)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		c := hits[i]
		label := "/" + c.Name
		if lipgloss.Width(label) > labelW {
			label = truncateWidth(label, labelW)
		}
		pad := labelW - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		descW := w - 2 - labelW - slashLabelGap
		desc := c.Help
		if descW > 0 {
			desc = truncateWidth(desc, descW)
		} else {
			desc = ""
		}
		prefix := "  "
		if i == m.cursor {
			prefix = slashPromptArrow
		}
		line := prefix + label + strings.Repeat(" ", pad)
		if desc != "" {
			line += strings.Repeat(" ", slashLabelGap) + desc
		}
		if i == m.cursor {
			line = selStyle.UnsetPadding().Render(line)
		} else {
			line = dimStyle.Render(line)
		}
		b.WriteString(line)
		if i+1 < end {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func slashLabelColumnWidth(hits []slashCmd, contentW int) int {
	budget := (contentW * 3 / 5)
	if budget > slashLabelCap {
		budget = slashLabelCap
	}
	maxW := 0
	for _, c := range hits {
		w := lipgloss.Width("/" + c.Name)
		if w <= slashLabelCap && w > maxW {
			maxW = w
		}
	}
	if maxW > budget {
		return budget
	}
	if maxW < 1 {
		return 1
	}
	return maxW
}

func truncateWidth(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := 1
		if r == '\t' {
			rw = 1
		}
		if w+rw >= n {
			b.WriteRune('…')
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}
