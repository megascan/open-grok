package pager

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/claudeimport"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/version"
)

func (m *model) hasClaudeImport() bool {
	if m.claudeDismissed {
		return false
	}
	return claudeimport.HasPendingImport()
}

func (m *model) openImportClaude() {
	plan := claudeimport.Scan(m.opts.CWD)
	m.importPlan = plan
	m.importSel = make([]bool, len(plan.Items))
	for i := range m.importSel {
		m.importSel[i] = true
	}
	m.overlay = overlayImportClaude
	m.cursor = 0
	m.rebuildImportItems()
}

func (m *model) rebuildImportItems() {
	m.items = nil
	if len(m.importPlan.Items) == 0 {
		m.items = []listItem{{Label: "No Claude settings found to import."}}
		return
	}
	for i, it := range m.importPlan.Items {
		mark := "[ ]"
		if i < len(m.importSel) && m.importSel[i] {
			mark = "[x]"
		}
		m.items = append(m.items, listItem{
			Key:    strconv.Itoa(i),
			Label:  mark + "  " + it.Label,
			Detail: it.Detail,
		})
	}
}

func (m *model) renderImportClaude(w int) string {
	body := titleStyle.Render("Import Claude settings") + "\n"
	body += dimStyle.Render("Writes into ~/.entropy (never ~/.grok). Credentials stay provider-isolated.") + "\n\n"
	if len(m.items) == 0 {
		body += dimStyle.Render("(empty)") + "\n"
	}
	start, limit := 0, 14
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	for i, it := range m.items {
		if i < start || i >= start+limit {
			continue
		}
		line := "  " + it.Label
		if it.Detail != "" {
			line += "  " + dimStyle.Render(it.Detail)
		}
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		}
		body += line + "\n"
	}
	body += "\n" + dimStyle.Render("space toggle  ·  a all  ·  n none  ·  enter import  ·  esc cancel")
	return modalBox.Width(w).Render(body)
}

func (m *model) handleImportClaudeKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		if m.screen == screenWelcome {
			m.refreshWelcome()
		}
		return nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return nil
	case "down", "j":
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		return nil
	case " ", "space":
		if m.cursor < len(m.importSel) {
			m.importSel[m.cursor] = !m.importSel[m.cursor]
			m.rebuildImportItems()
		}
		return nil
	case "a":
		for i := range m.importSel {
			m.importSel[i] = true
		}
		m.rebuildImportItems()
		return nil
	case "n":
		for i := range m.importSel {
			m.importSel[i] = false
		}
		m.rebuildImportItems()
		return nil
	case "enter":
		m.runClaudeImport()
		m.overlay = overlayNone
		if m.screen == screenWelcome {
			m.refreshWelcome()
		}
		return nil
	}
	return nil
}

func (m *model) dismissClaudeImport() {
	m.claudeDismissed = true
	_ = os.MkdirAll(config.Home(), 0o700)
	_ = os.WriteFile(filepath.Join(config.Home(), "claude-import-dismissed"), []byte("1\n"), 0o600)
	m.refreshWelcome()
}

func (m *model) runClaudeImport() {
	var picked []claudeimport.Item
	for i, it := range m.importPlan.Items {
		if i < len(m.importSel) && m.importSel[i] {
			picked = append(picked, it)
		}
	}
	res, err := claudeimport.Apply(m.opts.CWD, picked)
	if err != nil {
		m.status = err.Error()
		m.appendSys(err.Error())
		return
	}
	m.claudeDismissed = true
	msg := fmt.Sprintf("Imported %d Claude setting(s) into ~/.entropy (%d global, %d project). Credentials stay provider-isolated.",
		res.Total(), res.GlobalCount, res.ProjectCount)
	m.status = msg
	m.appendSys(msg)
}

func changelogBullets() []string {
	return []string{
		"Entropy " + version.String() + " — native Go pager",
		"Dashboard peek-reply, grouped tools, OSC 52 copy",
		"Folder trust, worktree, and Claude import on welcome",
		"Credentials stay under ~/.entropy, never ~/.grok",
	}
}

func (m *model) openReleaseNotes() {
	m.overlay = overlayReleaseNotes
	m.items = nil
	m.items = append(m.items, listItem{Label: "Entropy " + version.String(), Detail: "Native Go pager matching the Rust slash catalog and TUI surfaces."})
	for _, b := range changelogBullets() {
		m.items = append(m.items, listItem{Label: "• " + b})
	}
	m.cursor = 0
}
