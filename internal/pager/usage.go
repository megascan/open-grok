package pager

import (
	"fmt"
	"strconv"
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
	writeAccent(&b, strings.Join(heads, " "))
	writeAccent(&b, dimStyle.Render(usageShortcutHint))
	writeAccent(&b, "")
	var body string
	switch m.usageTab {
	case 0:
		body = m.contextReport()
	case 1:
		body = m.usageLimitReport()
	default:
		body = m.sessionInfo()
	}
	writeAccentLines(&b, strings.TrimRight(body, "\n"))
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
	case "y", "c":
		if m.sess != nil {
			return m.copyText(m.sess.ID, false)
		}
	}
	return nil
}

func (m *model) openCache() {
	m.overlay = overlayCache
	m.filter = ""
	m.items = nil
	m.cursor = 0
}

func (m *model) renderCache(w int) string {
	var b strings.Builder
	writeAccent(&b, titleStyle.Render("Prompt cache"))
	writeAccent(&b, "")
	writeAccentLines(&b, strings.TrimRight(m.cacheReport(), "\n"))
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("esc close"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) cacheReport() string {
	if m.sess == nil || len(m.sess.Messages) == 0 {
		return "Prompt cache telemetry: no turns recorded yet in this session."
	}
	bytes := 0
	turns := 0
	var prompts []string
	for _, msg := range m.sess.Messages {
		bytes += len(msg.Content)
		if msg.Role == types.RoleUser {
			turns++
			prompts = append(prompts, msg.Content)
		}
	}
	tokens := bytes / 4
	var b strings.Builder
	b.WriteString("Prompt Cache Telemetry & Diagnostics:\n")
	b.WriteString("  All-provider hit rate: n/a (Entropy has no sampler cache telemetry yet)\n")
	b.WriteString("  Supported hit rate:    n/a (no cache-reporting requests)\n")
	fmt.Fprintf(&b, "  Turns tracked:  %d (local conversation turns)\n", turns)
	fmt.Fprintf(&b, "  Prompt bytes:   %s\n", groupThousands(bytes))
	fmt.Fprintf(&b, "  Estimated tokens: %s (bytes/4)\n", groupThousands(tokens))
	if reuse := prefixReusePct(prompts); reuse >= 0 {
		fmt.Fprintf(&b, "  Prefix reuse:   %.1f%% (shared prefix of consecutive user prompts)\n", reuse)
	}
	return b.String()
}

func groupThousands(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := strconv.Itoa(n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return sign + strings.Join(parts, ",")
}

func prefixReusePct(prompts []string) float64 {
	if len(prompts) < 2 {
		return -1
	}
	var sum float64
	pairs := 0
	for i := 1; i < len(prompts); i++ {
		a, b := prompts[i-1], prompts[i]
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		den := len(a)
		if len(b) > den {
			den = len(b)
		}
		if den == 0 {
			continue
		}
		sum += float64(n) / float64(den) * 100
		pairs++
	}
	if pairs == 0 {
		return -1
	}
	return sum / float64(pairs)
}
