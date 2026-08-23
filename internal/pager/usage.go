package pager

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
)

func (m *model) openUsage(tab int) {
	m.overlay = overlayUsage
	m.usageTab = tab % 3
	m.cursor = 0
}

func (m *model) renderUsage(w int) string {
	tabs := []string{"Context usage", "Usage limit", "Session info"}
	var heads []string
	for i, t := range tabs {
		if i == m.usageTab {
			heads = append(heads, selStyle.Render(" "+t+" "))
		} else {
			heads = append(heads, dimStyle.Render(" "+t+" "))
		}
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Usage") + "\n")
	b.WriteString(strings.Join(heads, " ") + "\n")
	b.WriteString(dimStyle.Render("h/l tabs  ·  y copy session id  ·  esc") + "\n\n")
	switch m.usageTab {
	case 0:
		b.WriteString(m.contextReport())
	case 1:
		b.WriteString(m.usageLimitReport())
	default:
		b.WriteString(m.sessionInfo())
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) usageLimitReport() string {
	var b strings.Builder
	b.WriteString("Usage limit\n\n")
	st := auth.Status()
	any := false
	for _, p := range types.AllProviders {
		s, ok := st[p]
		if !ok {
			continue
		}
		any = true
		fmt.Fprintf(&b, "%s  ·  %s\n", p.DisplayName(), s)
		b.WriteString(dimStyle.Render("  quota is provider-hosted — open the provider console") + "\n")
	}
	if !any {
		b.WriteString(dimStyle.Render("signed out — usage unavailable") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Entropy does not proxy xAI Build credits. Non-xAI providers bill at their own meters."))
	return b.String()
}

func (m *model) handleUsageKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "left", "h":
		m.usageTab = (m.usageTab + 2) % 3
	case "right", "l", "tab":
		m.usageTab = (m.usageTab + 1) % 3
	case "1":
		m.usageTab = 0
	case "2":
		m.usageTab = 1
	case "3":
		m.usageTab = 2
	case "y":
		if m.sess != nil {
			return m.copyText(m.sess.ID, false)
		}
	}
	return nil
}
