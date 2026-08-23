package pager

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MAX_VISIBLE_SUGGESTIONS matches crates/.../slash/mod.rs.
const maxVisibleSuggestions = 6

// MAX_DROPDOWN_ROWS matches views/file_search/dropdown.rs.
const maxFileDropdownRows = 8

const (
	slashLabelCap = 40
	slashLabelGap = 2
	// promptArrow is glyphs::prompt_arrow(): U+276F plus space (2 columns).
	promptArrow = "\u276F "
	// historySearchPrefix is the in-prompt history-search indicator.
	historySearchPrefix = "? "
	// rememberPrefix is PromptInputMode::Remember (agent_view/mod.rs).
	rememberPrefix = "# "
)

// dropdownRow is one inline completion row (slash commands, args, @ files, history).
type dropdownRow struct {
	Label  string
	Help   string
	Insert string // composer replacement on Tab
	Run    string // slash line on Enter; empty means Insert
	Marks  []int  // nucleo highlight indices into Label
}

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
	rows := m.slashDropdownRows()
	if len(rows) == 0 {
		d := parseSlashDraft(m.ta.Value())
		if d.InArgs && d.OK {
			return ""
		}
		return dimStyle.Render("  (no matches)")
	}
	return renderDropdownRows(rows, m.cursor, maxVisibleSuggestions, m.transcriptWidth())
}

func (m *model) renderFileDropdown() string {
	if m.overlay != overlayFiles {
		return ""
	}
	rows := make([]dropdownRow, 0, len(m.items))
	for _, it := range m.items {
		rows = append(rows, dropdownRow{Label: it.Label, Help: it.Detail})
	}
	return renderDropdownRows(rows, m.cursor, maxFileDropdownRows, m.transcriptWidth())
}

func (m *model) renderHistoryDropdown() string {
	if m.overlay != overlayHistory {
		return ""
	}
	rows := make([]dropdownRow, 0, len(m.items))
	for _, it := range m.items {
		rows = append(rows, dropdownRow{Label: it.Label, Help: it.Detail, Marks: it.Marks})
	}
	if len(rows) == 0 {
		return dimStyle.Render("  (no matches)")
	}
	return renderDropdownRows(rows, m.cursor, maxFileDropdownRows, m.transcriptWidth())
}

func clampMarks(marks []int, s string) []int {
	n := len([]rune(s))
	var out []int
	for _, i := range marks {
		if i >= 0 && i < n {
			out = append(out, i)
		}
	}
	return out
}

func renderDropdownRows(rows []dropdownRow, selected, maxRows, width int) string {
	if len(rows) == 0 {
		return dimStyle.Render("  (no matches)")
	}
	if selected >= len(rows) {
		selected = len(rows) - 1
	}
	if selected < 0 {
		selected = 0
	}
	w := width
	if w < 8 {
		w = 8
	}
	labelW := dropdownLabelColumnWidth(rows, w)
	start := 0
	if selected >= maxRows {
		start = selected - maxRows + 1
	}
	end := start + maxRows
	if end > len(rows) {
		end = len(rows)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		r := rows[i]
		label := r.Label
		if len(r.Marks) > 0 {
			label = highlightMarks(label, clampMarks(r.Marks, label),
				func(s string) string { return titleStyle.Bold(true).Render(s) },
				nil)
		}
		if lipgloss.Width(label) > labelW {
			label = truncateWidth(label, labelW)
		}
		pad := labelW - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		descW := w - 2 - labelW - slashLabelGap
		desc := r.Help
		if descW > 0 {
			desc = truncateWidth(desc, descW)
		} else {
			desc = ""
		}
		prefix := "  "
		if i == selected {
			prefix = promptArrow
		}
		line := prefix + label + strings.Repeat(" ", pad)
		if desc != "" {
			line += strings.Repeat(" ", slashLabelGap) + desc
		}
		if i == selected {
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

func dropdownLabelColumnWidth(rows []dropdownRow, contentW int) int {
	budget := (contentW * 3 / 5)
	if budget > slashLabelCap {
		budget = slashLabelCap
	}
	maxW := 0
	for _, r := range rows {
		w := lipgloss.Width(r.Label)
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

func slashLabelColumnWidth(hits []slashCmd, contentW int) int {
	rows := make([]dropdownRow, len(hits))
	for i, c := range hits {
		rows[i] = dropdownRow{Label: "/" + c.Name}
	}
	return dropdownLabelColumnWidth(rows, contentW)
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
		if w+rw >= n {
			b.WriteRune('…')
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

func (m *model) isPromptOverlay() bool {
	return m.overlay == overlayPalette || m.overlay == overlayFiles || m.overlay == overlayHistory
}
