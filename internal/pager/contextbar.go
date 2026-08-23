package pager

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// contextBarSep is the Rust context_bar::SEPARATOR between status-bar items.
const contextBarSep = "│"

// fmtPct5 formats a usage percentage as a fixed-width 5-char string.
// Matches Rust views/context_bar.rs::fmt_pct5.
func fmtPct5(pct float64) string {
	switch {
	case pct >= 100:
		return "MAX %"
	case pct < 10:
		return fmt.Sprintf("%.2f%%", pct)
	default:
		return fmt.Sprintf("%.1f%%", pct)
	}
}

func usagePct(used, total int) float64 {
	if total <= 0 {
		return 0
	}
	pct := float64(used) / float64(total) * 100
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func contextPctStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= 85:
		return errStyle
	case pct >= 65:
		return toolStyle
	default:
		return titleStyle
	}
}

// contextBarText renders compact `8.5K / 1.0M` or, when hovered, a same-width
// progress bar plus fmtPct5 (`█████ 42.0%`). Matches context_bar_line.
func contextBarText(used, total int, hovered bool) string {
	if total <= 0 {
		return "0 / 0"
	}
	pct := usagePct(used, total)
	tokenStr := fmt.Sprintf("%s / %s", fmtTokens(used), fmtTokens(total))
	for utf8.RuneCountInString(tokenStr) < 6 {
		tokenStr += " "
	}
	width := utf8.RuneCountInString(tokenStr)
	style := contextPctStyle(pct)
	if !hovered {
		return style.Render(tokenStr)
	}
	barW := width - 6 // gap (1) + percentage (5)
	if barW < 0 {
		barW = 0
	}
	fill := int(math.Round(float64(barW) * pct / 100))
	if fill < 0 {
		fill = 0
	}
	if fill > barW {
		fill = barW
	}
	bar := strings.Repeat("█", fill) + strings.Repeat("░", barW-fill)
	return style.Render(bar) + " " + dimStyle.Render(fmtPct5(pct))
}

func contextBarNatural(used, total int) string {
	if total <= 0 {
		return "0 / 0"
	}
	s := fmt.Sprintf("%s / %s", fmtTokens(used), fmtTokens(total))
	for utf8.RuneCountInString(s) < 6 {
		s += " "
	}
	return s
}
