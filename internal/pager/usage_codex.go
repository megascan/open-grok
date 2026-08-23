package pager

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
)

type usageFetchedMsg struct {
	text string
}

var fetchCodexUsage = auth.FetchCodexUsage

func (m *model) fetchUsageCmd() tea.Cmd {
	return func() tea.Msg {
		xai := xaiUsageSummary()
		snap, err := fetchCodexUsage(context.Background())
		var codex string
		if err != nil {
			body := formatCodexUsageError(err.Error())
			if acc := auth.CodexAccountFromStore(); acc != nil {
				if header := formatCodexAccountHeader(*acc); header != "" {
					codex = header + "\n" + body
				} else {
					codex = body
				}
			} else {
				codex = body
			}
		} else {
			codex = formatCodexUsageSummary(snap)
		}
		return usageFetchedMsg{text: formatCombinedUsageSummary(xai, codex)}
	}
}
