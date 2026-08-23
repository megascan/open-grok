package pager

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Matches crates/codegen/xai-grok-pager/src/app/dispatch/status.rs.
const gboomNoGraphicsToast = "No demons here \u2014 GBOOM needs a graphics-capable terminal " +
	"(kitty, Ghostty, WezTerm, iTerm2)"

var gboomRed = lipgloss.NewStyle().Foreground(lipgloss.Color("#eb281f")).Bold(true)

type gboomHUD struct {
	HP           int
	Kills, Total int
	Playing      bool
}

func detectGraphicsProtocol() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("ITERM_SESSION_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || strings.Contains(term, "wezterm") {
		return true
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "iterm.app", "wezterm", "ghostty", "kitty":
		return true
	}
	return false
}

func (m *model) openGboom() {
	m.overlay = overlayGboom
	m.gboom = &gboomHUD{HP: 100, Total: 8}
}

func (m *model) handleGboomKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.overlay = overlayNone
		m.gboom = nil
	}
	return nil
}

func (m *model) renderGboom() string {
	hud := gboomHUD{HP: 100, Total: 8}
	if m.gboom != nil {
		hud = *m.gboom
	}
	w := max(30, m.width*90/100)
	h := max(8, m.height*90/100)
	if w > m.width-2 {
		w = max(30, m.width-2)
	}
	title := gboomRed.Render(" GBOOM ")
	inner := strings.Repeat("\n", max(1, h-6))
	inner += dimStyle.Render("KNEE-DEEP IN THE TOKENS")
	hpStyle := okStyle
	if hud.HP <= 30 {
		hpStyle = errStyle
	} else if hud.HP <= 60 {
		hpStyle = warnStyle
	}
	stats := hpStyle.Bold(true).Render(fmt.Sprintf(" HP %-3d · KILLS %d/%d ", hud.HP, hud.Kills, hud.Total))
	hint := " ESC quit "
	if hud.Playing {
		hint = " WASD/\u2190\u2192 move · SPACE fire · ESC quit "
	}
	bar := stats + strings.Repeat(" ", max(1, w-4-lipgloss.Width(stats)-len([]rune(hint)))) + dimStyle.Render(hint)
	body := title + "\n" + inner + "\n" + bar
	return modalBox.Width(w).Render(body)
}
