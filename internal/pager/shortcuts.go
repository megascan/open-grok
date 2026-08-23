package pager

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type shortcutRow struct {
	Cat, Keys, Label, Help string
	Header                 bool
}

func shortcutCatalog() []shortcutRow {
	return []shortcutRow{
		{Cat: "Essentials", Header: true},
		{Keys: "enter", Label: "Send", Help: "Submit the prompt. In multiline mode, Alt+Enter sends and Enter inserts a newline."},
		{Keys: "esc", Label: "Cancel / park", Help: "Park a permission or question card, cancel a turn, or close the current overlay."},
		{Keys: "ctrl+c", Label: "Interrupt", Help: "Cancel the running turn, or quit when the prompt is empty."},
		{Keys: quitChord(), Label: "Quit", Help: "Leave Entropy. VS Code-family terminals use Ctrl+D so Ctrl+Q stays available to the editor."},
		{Keys: "ctrl+.  ctrl+x  ?", Label: "Shortcuts", Help: "Open this cheatsheet. Search by typing; Enter expands the selected row."},
		{Keys: "/", Label: "Slash commands", Help: "Open the command palette. Tab completes; Enter runs the highlighted command."},
		{Cat: "Input", Header: true},
		{Keys: "ctrl+v", Label: "Paste", Help: "Paste clipboard text (and image chips when the terminal delivers them)."},
		{Keys: "alt+enter", Label: "Newline / send", Help: "Insert a newline in single-line mode; send in multiline mode."},
		{Keys: "up", Label: "Prompt history", Help: "Open fuzzy prompt history when the draft is empty."},
		{Keys: "@", Label: "File search", Help: "Attach a workspace path. Tab accepts the highlighted hit."},
		{Keys: "shift+tab", Label: "Cycle permission mode", Help: "Ask → plan → always-approve → ask."},
		{Cat: "Conversation Navigation", Header: true},
		{Keys: "tab", Label: "Focus scrollback", Help: "Blur the prompt and walk blocks with j/k."},
		{Keys: "j / k", Label: "Next / previous block", Help: "Move the selected scrollback block when the prompt is blurred."},
		{Keys: "y", Label: "Copy block", Help: "Copy the selected (or latest) agent reply via OSC 52 + ~/.entropy/last-copy.txt."},
		{Keys: "e", Label: "Expand / fold", Help: "Toggle the selected block fold, including grouped tool verbs."},
		{Keys: "/find", Label: "Find in scrollback", Help: "Search transcript text and jump to the match."},
		{Keys: "/jump  /timeline", Label: "Jump to turn", Help: "Pick a user turn and scroll it into view."},
		{Cat: "Conversation Actions", Header: true},
		{Keys: "/rewind", Label: "Rewind", Help: "Pick a previous user turn, restore it into the draft, and drop later messages."},
		{Keys: "/btw", Label: "Aside", Help: "Ask a side question without interrupting the running turn."},
		{Keys: "/compact", Label: "Compact", Help: "Compress history to reclaim context."},
		{Keys: "g", Label: "Goal detail", Help: "Open the expanded goal overlay (token budget, progress, history)."},
		{Cat: "Panels", Header: true},
		{Keys: "ctrl+\\", Label: "Dashboard", Help: "Open the live session roster. Esc returns without clearing the draft."},
		{Keys: "/settings", Label: "Settings", Help: "Toggle the same registry rows as the Rust settings modal."},
		{Keys: "/mcps", Label: "MCP servers", Help: "List local [mcp_servers] and live tools."},
		{Keys: "/queue", Label: "Follow-up queue", Help: "Reorder or drop mid-turn follow-ups (#1, #2, …)."},
		{Cat: "Session", Header: true},
		{Keys: "/new", Label: "New session", Help: "Start a fresh conversation in this directory."},
		{Keys: "/resume", Label: "Resume", Help: "Grouped picker across every ~/.entropy/sessions cwd, plus Claude/Codex jsonl."},
		{Keys: "/usage", Label: "Usage", Help: "Tabbed context / usage-limit / session-info modal."},
		{Cat: "Dashboard", Header: true},
		{Keys: "enter", Label: "Attach", Help: "Attach the highlighted session from the roster."},
		{Keys: "1–9", Label: "Peek options", Help: "Select a peek question option without submitting until Enter."},
	}
}

func (m *model) openShortcuts() {
	m.overlay = overlayShortcuts
	m.cursor = 0
	m.filter = ""
	m.shortcutExpand = -1
	m.items = m.shortcutItems("")
}

func (m *model) shortcutItems(filter string) []listItem {
	f := strings.ToLower(strings.TrimSpace(filter))
	var out []listItem
	for i, r := range shortcutCatalog() {
		if r.Header {
			if f != "" {
				continue
			}
			out = append(out, listItem{Key: "hdr:" + r.Cat, Label: r.Cat, Detail: "section"})
			continue
		}
		blob := strings.ToLower(r.Keys + " " + r.Label + " " + r.Help + " " + r.Cat)
		if f != "" && !strings.Contains(blob, f) && scoreName(f, r.Label) == 0 && scoreName(f, r.Keys) == 0 {
			continue
		}
		out = append(out, listItem{Key: fmt.Sprintf("sc:%d", i), Label: r.Keys + "   " + r.Label, Detail: r.Help})
	}
	return out
}

func (m *model) renderShortcuts(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Shortcuts") + "\n")
	if m.filter != "" {
		b.WriteString(dimStyle.Render("filter: "+m.filter) + "\n")
	}
	b.WriteString(dimStyle.Render("type to search  ·  enter expand  ·  esc close") + "\n\n")
	if len(m.items) == 0 {
		b.WriteString(dimStyle.Render("(no matches)") + "\n")
	}
	start, limit := 0, 16
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	for i, it := range m.items {
		if i < start || i >= start+limit {
			continue
		}
		if strings.HasPrefix(it.Key, "hdr:") {
			b.WriteString(titleStyle.Render(it.Label) + "\n")
			continue
		}
		line := it.Label
		if i == m.cursor {
			line = selStyle.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
		if i == m.cursor && m.shortcutExpand == i {
			b.WriteString(dimStyle.Render("    "+it.Detail) + "\n")
		}
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handleShortcutsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+x", "ctrl+.":
		m.overlay = overlayNone
		m.filter = ""
		return nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.shortcutExpand = -1
	case "down", "j":
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		m.shortcutExpand = -1
	case "enter", "l", "right":
		if m.shortcutExpand == m.cursor {
			m.shortcutExpand = -1
		} else {
			m.shortcutExpand = m.cursor
		}
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.items = m.shortcutItems(m.filter)
			m.cursor = 0
		}
	}
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
		m.items = m.shortcutItems(m.filter)
		m.cursor = 0
		m.shortcutExpand = -1
	}
	return nil
}
