package pager

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var pathChip = lipgloss.NewStyle().
	Foreground(lipgloss.Color("39")).
	Background(lipgloss.Color("237")).
	Padding(0, 1)

func (m *model) rememberChip(path string, isDir bool) {
	if isDir || path == "" {
		return
	}
	for _, c := range m.chips {
		if c == path {
			return
		}
	}
	m.chips = append(m.chips, path)
}

func (m *model) renderChips() string {
	if len(m.chips) == 0 {
		return ""
	}
	var parts []string
	for _, c := range m.chips {
		parts = append(parts, pathChip.Render("@"+c))
	}
	return dimStyle.Render("@") + " " + strings.Join(parts, " ") + "\n"
}

func chipPathsIn(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "@") && len(f) > 1 {
			out = append(out, strings.TrimPrefix(f, "@"))
		}
	}
	return out
}
