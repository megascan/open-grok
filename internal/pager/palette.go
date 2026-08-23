package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// defaultPaletteEntries is the grouped Ctrl+P / /help command palette,
// matching views/modal.rs::default_palette_entries.
func defaultPaletteEntries(screenMode string) []listItem {
	entries := []listItem{
		{Key: "section:session", Label: "Session", Header: true},
		{Key: "new", Label: "New Session", Detail: "Ctrl+N"},
		{Key: "new-worktree", Label: "New Session in Worktree", Detail: "Ctrl+P → worktree"},
		{Key: "slash:/dashboard", Label: "Agent Dashboard", Detail: "/dashboard"},
		{Key: "home", Label: "Back to Home", Detail: "/home"},
		{Key: "slash:/delete", Label: "Delete This Session", Detail: "/delete"},
		{Key: "slash:/resume", Label: "Resume Session", Detail: "/resume"},
		{Key: "slash:/share", Label: "Share Session", Detail: "/share"},
		{Key: "slash:/rename ", Label: "Rename Session", Detail: "/rename "},
		{Key: "slash:/session-info", Label: "Session Info", Detail: "/session-info"},
		{Key: "slash:/feedback ", Label: "Send Feedback", Detail: "/feedback "},
		{Key: "section:context", Label: "Context", Header: true},
		{Key: "slash:/compact", Label: "Compact History", Detail: "/compact"},
		{Key: "slash:/context", Label: "Context Usage", Detail: "/context"},
		{Key: "slash:/view-plan", Label: "View Plan", Detail: "/view-plan"},
		{Key: "memory", Label: "Memory", Detail: "/memory"},
		{Key: "section:model", Label: "Model & Input", Header: true},
		{Key: "slash:/model ", Label: "Switch Model", Detail: "/model"},
		{Key: "slash:/always-approve", Label: "Always Approve Mode", Detail: "/always-approve"},
		{Key: "slash:/multiline", Label: "Multiline Input", Detail: "/multiline"},
		{Key: "section:tools", Label: "Tools", Header: true},
		{Key: "ext:0", Label: "Hooks", Detail: "/hooks"},
		{Key: "ext:1", Label: "Plugins", Detail: "/plugins"},
		{Key: "ext:2", Label: "Marketplace", Detail: "/marketplace"},
		{Key: "ext:3", Label: "Skills", Detail: "/skills"},
		{Key: "ext:4", Label: "MCP Servers", Detail: "/mcps"},
		{Key: "agents", Label: "Manage Agents", Detail: "/config-agents"},
		{Key: "section:other", Label: "Other", Header: true},
		{Key: "slash:/theme ", Label: "Switch Theme", Detail: "/theme"},
		{Key: "settings", Label: "Settings", Detail: "F2"},
		{Key: "shortcuts", Label: "Keyboard Shortcuts", Detail: "Ctrl+."},
		{Key: "docs", Label: "How-to Guides", Detail: "/docs"},
		{Key: "slash:/tutorial", Label: "Tutorial", Detail: "/tutorial"},
		{Key: "quit", Label: "Quit", Detail: "Ctrl+Q"},
	}
	if screenMode == modeMinimal {
		// Rust only shows the external-editor row in minimal mode.
		out := make([]listItem, 0, len(entries)+1)
		for _, e := range entries {
			out = append(out, e)
			if e.Key == "slash:/multiline" {
				out = append(out, listItem{
					Key:    "edit-prompt",
					Label:  "Edit Prompt in External Editor",
					Detail: "Ctrl+G",
				})
			}
		}
		entries = out
	}
	keep := make([]listItem, 0, len(entries))
	for _, e := range entries {
		if e.Header || paletteSlashSupported(e.Key, screenMode) {
			keep = append(keep, e)
		}
	}
	return pruneEmptyPaletteSections(keep)
}

func paletteSlashSupported(key, screenMode string) bool {
	cmd, ok := strings.CutPrefix(key, "slash:")
	if !ok {
		return true
	}
	name := strings.TrimSpace(strings.TrimPrefix(cmd, "/"))
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	c, found := ResolveSlash(name)
	if !found || c.Mode == "" {
		return true
	}
	return c.Mode == screenMode
}

func pruneEmptyPaletteSections(entries []listItem) []listItem {
	var out []listItem
	for i, e := range entries {
		if !e.Header {
			out = append(out, e)
			continue
		}
		hasItem := false
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Header {
				break
			}
			hasItem = true
			break
		}
		if hasItem {
			out = append(out, e)
		}
	}
	return out
}

// filterPaletteEntries keeps section headers when any item in that section matches.
func filterPaletteEntries(query string, all []listItem) []listItem {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all
	}
	var result []listItem
	var pending *listItem
	for i := range all {
		e := all[i]
		if e.Header {
			pending = &all[i]
			continue
		}
		if strings.Contains(strings.ToLower(e.Label), q) || strings.Contains(strings.ToLower(e.Detail), q) {
			if pending != nil {
				result = append(result, *pending)
				pending = nil
			}
			result = append(result, e)
		}
	}
	return result
}

func (m *model) toggleCommandPalette() tea.Cmd {
	if m.overlay == overlayHelp {
		m.overlay = overlayNone
		m.filter = ""
		return nil
	}
	m.openHelp()
	return nil
}

func (m *model) firstSelectableItem() {
	for i, it := range m.items {
		if !it.Header {
			m.cursor = i
			return
		}
	}
	m.cursor = 0
}

func (m *model) stepSelectableCursor(delta int) {
	n := len(m.items)
	if n == 0 {
		return
	}
	i := m.cursor + delta
	for i >= 0 && i < n {
		if !m.items[i].Header {
			m.cursor = i
			return
		}
		i += delta
	}
}

func (m *model) runPaletteItem(it listItem) tea.Cmd {
	if it.Header {
		return nil
	}
	m.overlay = overlayNone
	m.filter = ""
	switch it.Key {
	case "new":
		return m.handleNew()
	case "new-worktree":
		return m.startNewSession(true)
	case "home":
		return m.runSlash("home", "")
	case "memory":
		return m.handleMemory("")
	case "settings":
		m.openSettings()
		return nil
	case "shortcuts":
		m.openShortcuts()
		return nil
	case "docs":
		m.openDocs()
		return nil
	case "agents":
		m.openAgents(agentsTabAgents)
		return nil
	case "edit-prompt":
		return m.editPrompt()
	case "quit":
		return tea.Quit
	}
	if strings.HasPrefix(it.Key, "ext:") {
		tab := 0
		switch it.Key {
		case "ext:1":
			tab = 1
		case "ext:2":
			tab = 2
		case "ext:3":
			tab = 3
		case "ext:4":
			m.openMCP()
			return nil
		}
		m.openExtensions(tab)
		return nil
	}
	if cmd, ok := strings.CutPrefix(it.Key, "slash:"); ok {
		if strings.HasSuffix(cmd, " ") {
			m.ta.SetValue(cmd)
			m.ta.Focus()
			return nil
		}
		return m.handleSlash(cmd)
	}
	return nil
}
