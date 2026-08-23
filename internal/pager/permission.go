package pager

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/workspace"
)

type permChoice struct {
	ID    string // allow, always, always-cmd, never, followup
	Label string
}

func (m *model) permCommand() string {
	if m.perm == nil {
		return ""
	}
	if m.perm.Command != "" {
		return m.perm.Command
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(m.perm.Args), &parsed) != nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "script"} {
		if s, ok := parsed[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func (m *model) permWords() []string {
	return bashWords(m.permCommand())
}

func (m *model) permScopedCmd() string {
	words := m.permWords()
	if len(words) == 0 {
		return m.permCommand()
	}
	n := m.permWordN
	if n < 1 {
		n = 1
	}
	if n > len(words) {
		n = len(words)
	}
	return strings.Join(words[:n], " ")
}

func (m *model) permMCPServerName() string {
	if m.perm == nil {
		return ""
	}
	if s := strings.TrimSpace(m.perm.Server); s != "" {
		return s
	}
	tool := m.perm.Tool
	if server, _, ok := strings.Cut(tool, workspace.MCPToolNameDelimiter); ok && server != "" && !strings.Contains(server, " ") {
		return server
	}
	return ""
}

func (m *model) permMCPQualified() string {
	server := m.permMCPServerName()
	if server == "" || m.perm == nil {
		return ""
	}
	if strings.Contains(m.perm.Tool, workspace.MCPToolNameDelimiter) {
		return m.perm.Tool
	}
	action := m.perm.Tool
	prefix := "mcp_" + server + "_"
	if strings.HasPrefix(action, prefix) {
		action = strings.TrimPrefix(action, prefix)
	} else if strings.HasPrefix(action, "mcp_") {
		action = strings.TrimPrefix(action, "mcp_")
		action = strings.TrimPrefix(action, server+"_")
	}
	return server + workspace.MCPToolNameDelimiter + action
}

func (m *model) permAlwaysLabel() string {
	server := m.permMCPServerName()
	if m.perm != nil && m.perm.Access == "mcp" && server != "" {
		if m.permMCPServer {
			return "Always allow: all tools from " + workspace.MCPTitleizeSegment(server)
		}
		name := workspace.MCPToolDisplayName(m.permMCPQualified(), server)
		return "Always allow: " + name
	}
	return "Always allow this tool"
}

func (m *model) permChoices() []permChoice {
	if m.perm == nil {
		return nil
	}
	cmd := m.permScopedCmd()
	bash := m.perm.Access == "bash" && strings.TrimSpace(cmd) != ""
	mcp := m.perm.Access == "mcp"
	var out []permChoice
	if bash {
		out = append(out, permChoice{ID: "always-cmd", Label: "Always allow: " + cmd})
	}
	if mcp {
		out = append(out, permChoice{ID: "always", Label: m.permAlwaysLabel()})
	}
	if m.perm.Access == "edit" {
		out = append(out, permChoice{ID: "allow-edits-session", Label: "Allow all edits this session"})
	}
	out = append(out, permChoice{ID: "allow", Label: "Yes, proceed"})
	if bash {
		out = append(out, permChoice{ID: "never-cmd", Label: "Never allow: " + cmd})
	} else {
		if !mcp {
			out = append(out, permChoice{ID: "always", Label: "Always allow this tool"})
		}
		out = append(out, permChoice{ID: "never", Label: "Never allow this tool"})
	}
	out = append(out, permChoice{ID: "followup", Label: permFollowupLabel})
	return out
}

func (m *model) initPermCard() {
	m.permFollowup = ""
	m.permFollowupMode = false
	m.permPatternMode = false
	m.permPattern = ""
	m.permMCPServer = false
	m.permExpanded = false
	words := m.permWords()
	m.permWordN = len(words)
	if m.permWordN == 0 {
		m.permWordN = 1
	}
	want := strings.ToLower(m.cfg.UI.DefaultPermChoice)
	choices := m.permChoices()
	m.permCursor = 0
	for i, c := range choices {
		switch want {
		case "always":
			if c.ID == "always" || c.ID == "always-cmd" {
				m.permCursor = i
			}
		case "deny":
			if c.ID == "never" {
				m.permCursor = i
			}
		}
	}
}

func (m *model) permTitle() string {
	if m.perm == nil {
		return "Allow?"
	}
	switch m.perm.Access {
	case "edit":
		if p := strings.TrimSpace(m.perm.Path); p != "" {
			return "Allow Edit to " + p + "?"
		}
		return "Allow Edit?"
	case "bash":
		words := m.permWords()
		if len(words) > 0 {
			bin := words[0]
			if i := strings.LastIndex(bin, "/"); i >= 0 && i+1 < len(bin) {
				bin = bin[i+1:]
			}
			return "Allow `" + bin + "`?"
		}
		return "Allow Execute?"
	case "mcp":
		server := m.permMCPServerName()
		name := workspace.MCPToolDisplayName(m.permMCPQualified(), server)
		if name != "" {
			return "Allow " + name + "?"
		}
		if m.perm.Tool != "" {
			return "Allow " + m.perm.Tool + "?"
		}
		return "Allow?"
	default:
		if m.perm.Tool != "" {
			return "Allow " + m.perm.Tool + "?"
		}
		return "Allow?"
	}
}

func (m *model) renderPermission(w int) string {
	if m.perm == nil {
		return ""
	}
	park := ""
	if m.permParked {
		park = "\n" + dimStyle.Render("keyboard parked in scrollback — Tab returns")
	}
	var b strings.Builder
	writePerm := func(s string) {
		b.WriteString(titleStyle.Render(permAccentBar) + "  " + s + "\n")
	}
	writePerm(titleStyle.Render(m.permTitle()))
	writePerm("")
	if m.perm.Path != "" && m.perm.Access == "edit" {
		writePerm(dimStyle.Render(m.perm.Path))
	}
	cmd := m.permCommand()
	if cmd != "" {
		contentW := w - 10
		if contentW < 8 {
			contentW = 8
		}
		maxRows := 0
		if !m.permExpanded {
			maxRows = permissionCollapsedRows
		}
		rows, trunc := bashDisplayRows(cmd, contentW, maxRows)
		for _, row := range rows {
			writePerm(highlightBashCommand(row))
		}
		if trunc {
			writePerm(truncationIndicator())
		}
	}
	if m.perm.Access == "bash" || (m.perm.Access == "mcp" && m.permMCPServerName() != "") {
		hint := ""
		if m.perm.Access == "bash" && len(m.permWords()) > 1 {
			hint = "\u2190 \u2192 narrow scope"
			if strings.TrimSpace(m.permCommand()) != "" {
				hint += "  \u00b7  e edit pattern"
			}
		} else if m.perm.Access == "mcp" {
			hint = "\u2190 \u2192 narrow scope"
		} else if m.perm.Access == "bash" {
			hint = "e edit pattern"
		}
		if hint != "" {
			writePerm(dimStyle.Render(hint))
		}
	}
	writePerm("")
	choices := m.permChoices()
	if m.permCursor >= len(choices) {
		m.permCursor = 0
	}
	for i, c := range choices {
		marker := "(" + pickerDotEmpty + ")"
		label := c.Label
		if i == m.permCursor {
			marker = "(" + pickerDotFilled + ")"
		}
		if c.ID == "followup" {
			if m.permFollowupMode && m.permFollowup != "" {
				first := m.permFollowup
				if j := strings.IndexByte(first, '\n'); j >= 0 {
					first = first[:j]
				}
				label = first
			} else if m.permFollowupMode {
				label = permFollowupLabel
			}
		}
		if c.ID == "always-cmd" && m.permPatternMode {
			label = "Always allow: " + m.permPattern + "\u2588"
		}
		sc := permShortcut(i)
		line := fmt.Sprintf("%s %s %s", sc, marker, label)
		if i == m.permCursor {
			line = selStyle.Render(line)
		}
		writePerm(line)
	}
	writePerm("")
	writePerm(dimStyle.Render("1–9  ·  j/k  ·  enter  ·  esc park") + park)
	return modalBox.Width(w).Render(strings.TrimRight(b.String(), "\n"))
}

func permShortcut(index int) string {
	if index >= 0 && index < 9 {
		return string(rune('1' + index))
	}
	return " "
}

func (m *model) handlePermissionKey(msg tea.KeyMsg) tea.Cmd {
	if m.perm == nil {
		return nil
	}
	if m.permFollowupMode {
		switch msg.Type {
		case tea.KeyEsc:
			m.permFollowupMode = false
			return nil
		case tea.KeyEnter:
			text := strings.TrimSpace(m.permFollowup)
			m.permFollowupMode = false
			m.replyPerm(false, false, false)
			if text != "" {
				m.queue = append(m.queue, text)
			}
			return nil
		case tea.KeyBackspace:
			if len(m.permFollowup) > 0 {
				m.permFollowup = m.permFollowup[:len(m.permFollowup)-1]
			}
			return nil
		}
		if msg.Type == tea.KeyRunes {
			m.permFollowup += string(msg.Runes)
		}
		return nil
	}
	if m.permPatternMode {
		switch msg.Type {
		case tea.KeyEsc:
			m.permPatternMode = false
			return nil
		case tea.KeyEnter:
			m.permPatternMode = false
			if m.sess != nil && strings.TrimSpace(m.permPattern) != "" {
				m.sess.Allow = append(m.sess.Allow, "bash:"+m.permPattern)
			}
			m.replyPerm(true, true, false)
			return nil
		case tea.KeyBackspace:
			if len(m.permPattern) > 0 {
				m.permPattern = m.permPattern[:len(m.permPattern)-1]
			}
			return nil
		}
		if msg.Type == tea.KeyRunes {
			m.permPattern += string(msg.Runes)
		}
		return nil
	}
	choices := m.permChoices()
	s := msg.String()
	if idx := optionIndexFromKey(s, len(choices)); idx >= 0 {
		m.permCursor = idx
		return m.applyPermChoice(choices[idx])
	}
	switch s {
	case "up", "k":
		if m.permCursor > 0 {
			m.permCursor--
		}
	case "down", "j":
		if m.permCursor+1 < len(choices) {
			m.permCursor++
		}
	case "left", "h":
		if m.perm != nil && m.perm.Access == "mcp" && m.permMCPServerName() != "" {
			m.jumpPermAlways()
			m.permMCPServer = false
			break
		}
		if m.permWordN > 1 {
			m.permWordN--
		}
	case "right", "l":
		if m.perm != nil && m.perm.Access == "mcp" && m.permMCPServerName() != "" {
			m.jumpPermAlways()
			m.permMCPServer = true
			break
		}
		if n := len(m.permWords()); m.permWordN < n {
			m.permWordN++
		}
	case "e":
		m.permPatternMode = true
		m.permPattern = m.permScopedCmd()
	case "x":
		m.permFollowupMode = true
		for i, c := range choices {
			if c.ID == "followup" {
				m.permCursor = i
			}
		}
	case "y":
		return m.applyPermChoice(permChoice{ID: "allow"})
	case "a":
		return m.applyPermChoice(permChoice{ID: "always"})
	case "n":
		return m.applyPermChoice(permChoice{ID: "never"})
	case "enter":
		if m.permCursor < len(choices) {
			return m.applyPermChoice(choices[m.permCursor])
		}
	case "esc":
		m.permParked = true
		m.overlay = overlayNone
		m.focusScroll = true
		m.ta.Blur()
	case "tab":
		// stay in card
	case "ctrl+f":
		m.permExpanded = !m.permExpanded
	case "ctrl+e":
		if m.perm != nil {
			body := m.permCommand()
			if body == "" {
				body = m.perm.Args
			}
			m.openBlockViewer("Permission args", body)
		}
	}
	return nil
}

func (m *model) applyPermChoice(c permChoice) tea.Cmd {
	switch c.ID {
	case "allow":
		m.replyPerm(true, false, false)
	case "always":
		rule := m.perm.Tool
		if m.perm.Access == "mcp" && m.permMCPServer && m.permMCPServerName() != "" {
			rule = workspace.MCPServerAllowRule(m.permMCPServerName())
		}
		m.rememberPermRule(rule, false)
		if m.sess != nil {
			m.sess.Allow = append(m.sess.Allow, rule)
		}
		m.replyPerm(true, false, false)
	case "always-cmd":
		rule := "bash:" + m.permScopedCmd()
		if m.sess != nil {
			m.sess.Allow = append(m.sess.Allow, rule)
		}
		m.rememberPermRule(rule, false)
		m.replyPerm(true, false, false)
	case "allow-edits-session":
		if m.sess != nil {
			m.sess.AllowEditsSession = true
		}
		m.replyPerm(true, false, false)
	case "never":
		if m.sess != nil {
			m.sess.Deny = append(m.sess.Deny, m.perm.Tool)
		}
		m.rememberPermRule(m.perm.Tool, true)
		m.replyPerm(false, false, true)
	case "never-cmd":
		rule := "bash:" + m.permScopedCmd()
		if m.sess != nil {
			m.sess.Deny = append(m.sess.Deny, rule)
		}
		m.rememberPermRule(rule, true)
		m.replyPerm(false, false, true)
	case "followup":
		m.permFollowupMode = true
	}
	return nil
}

func (m *model) replyPerm(allow, always, never bool) {
	if m.perm == nil {
		return
	}
	if always && m.sess != nil {
		m.sess.Allow = append(m.sess.Allow, m.perm.Tool)
		if m.cfg.UI.RememberApprovals {
			m.appendSys("always allow " + m.perm.Tool + " (this session + remembered)")
		}
	}
	_ = never
	ch := m.perm.Reply
	m.perm = nil
	m.overlay = overlayNone
	m.permParked = false
	m.permFollowupMode = false
	m.permPatternMode = false
	if m.question != nil {
		m.overlay = overlayQuestion
	}
	if ch != nil {
		ch <- allow
	}
}

func (m *model) rememberPermRule(rule string, deny bool) {
	if m.perm == nil || !m.cfg.UI.RememberApprovals || strings.TrimSpace(rule) == "" {
		return
	}
	if deny {
		m.cfg.Permission.Deny = appendUniqRule(m.cfg.Permission.Deny, rule)
	} else {
		m.cfg.Permission.Allow = appendUniqRule(m.cfg.Permission.Allow, rule)
	}
	_ = config.Save(m.cfg)
}

func appendUniqRule(list []string, rule string) []string {
	for _, r := range list {
		if r == rule {
			return list
		}
	}
	return append(list, rule)
}

func (m *model) jumpPermAlways() {
	for i, c := range m.permChoices() {
		if c.ID == "always" || c.ID == "always-cmd" {
			m.permCursor = i
			return
		}
	}
}

func (m *model) openBlockViewer(title, body string) {
	m.blockTitle = title
	m.blockBody = body
	m.blockScroll = 0
	m.overlay = overlayBlock
}
