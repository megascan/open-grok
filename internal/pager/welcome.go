package pager

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

func (m *model) refreshWelcome() {
	m.items = nil
	m.items = append(m.items, listItem{Key: "new", Label: "New session", Detail: "start chatting in " + m.opts.CWD})
	list, _ := agent.ListSessions(m.opts.CWD)
	for i, s := range list {
		if i >= 12 {
			break
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		m.items = append(m.items, listItem{
			Key:    "resume:" + s.ID,
			Label:  shortID(s.ID) + "  " + title,
			Detail: s.UpdatedAt.Format("2006-01-02 15:04") + "  " + s.ModelID,
		})
	}
	m.items = append(m.items,
		listItem{Key: "login", Label: "Login", Detail: "API key or Codex OAuth — stored under ~/.opengrok"},
		listItem{Key: "settings", Label: "Settings", Detail: "theme, vim, timestamps, screen mode"},
		listItem{Key: "quit", Label: "Quit", Detail: "leave Open Grok"},
	)
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) viewWelcome() string {
	var b strings.Builder
	b.WriteString(headerBar.Width(max(20, m.width)).Render(" Open Grok  "+version.String()) + "\n\n")
	b.WriteString(titleStyle.Render("  Welcome") + "\n")
	b.WriteString(dimStyle.Render("  State: "+config.Home()+"   never ~/.grok") + "\n")
	b.WriteString(dimStyle.Render("  Model: "+m.opts.Model.Name+" via "+m.opts.Model.Provider.DisplayName()) + "\n\n")
	st := auth.Status()
	var signed []string
	for _, p := range types.AllProviders {
		if _, ok := st[p]; ok {
			signed = append(signed, p.DisplayName())
		}
	}
	if len(signed) == 0 {
		b.WriteString(errStyle.Render("  Not signed in — pick Login or run open-grok login") + "\n\n")
	} else {
		b.WriteString(okStyle.Render("  Signed in: "+strings.Join(signed, ", ")) + "\n\n")
	}
	for i, it := range m.items {
		line := fmt.Sprintf("  %s  %s", it.Label, dimStyle.Render(it.Detail))
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("  ↑↓ move  ·  enter  ·  d delete session  ·  q quit"))
	return b.String()
}

func (m *model) handleWelcomeKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
	case "q", "ctrl+c":
		return tea.Quit
	case "d":
		if m.cursor < len(m.items) && strings.HasPrefix(m.items[m.cursor].Key, "resume:") {
			m.confirmKind = "delete-welcome:" + strings.TrimPrefix(m.items[m.cursor].Key, "resume:")
			m.overlay = overlayConfirm
		}
	case "enter":
		if m.cursor >= len(m.items) {
			return nil
		}
		return m.pickWelcome(m.items[m.cursor].Key)
	}
	return nil
}

func (m *model) pickWelcome(key string) tea.Cmd {
	switch {
	case key == "new":
		sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, m.permMode == permAlways)
		if err != nil {
			m.status = err.Error()
			return nil
		}
		m.attachSession(sess)
		m.layoutChat()
	case strings.HasPrefix(key, "resume:"):
		id := strings.TrimPrefix(key, "resume:")
		sess, err := agent.LoadSession(m.opts.CWD, id)
		if err != nil {
			m.status = err.Error()
			return nil
		}
		m.attachSession(sess)
		m.layoutChat()
	case key == "login":
		m.openLogin("")
	case key == "settings":
		m.openSettings()
	case key == "quit":
		return tea.Quit
	}
	return nil
}

func (m *model) openLogin(provider string) {
	m.screen = screenLogin
	m.overlay = overlayNone
	m.keyBuf = ""
	m.loginKimiCode = false
	m.items = nil
	if provider != "" {
		p, ok := types.ParseProvider(provider)
		if ok {
			m.loginP = p
			if provider == "kimi-code" || provider == "kimi_code" {
				m.loginKimiCode = true
			}
			return
		}
	}
	m.loginP = ""
	for _, p := range types.AllProviders {
		detail := "API key → " + config.AuthJSON()
		if p == types.ProviderCodex {
			detail = "OAuth or API key → " + config.CodexAuthJSON()
		}
		m.items = append(m.items, listItem{Key: string(p), Label: p.DisplayName(), Detail: detail})
	}
	m.cursor = 0
}

func (m *model) viewLogin() string {
	var b strings.Builder
	b.WriteString(headerBar.Width(max(20, m.width)).Render(" Sign in  ·  ~/.opengrok only") + "\n\n")
	if m.loginP == "" {
		b.WriteString(titleStyle.Render("  Choose a provider") + "\n\n")
		for i, it := range m.items {
			line := fmt.Sprintf("  %s  %s", it.Label, dimStyle.Render(it.Detail))
			if i == m.cursor {
				line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("  enter select  ·  esc back"))
		return b.String()
	}
	b.WriteString(titleStyle.Render("  "+m.loginP.DisplayName()) + "\n")
	if m.loginP == types.ProviderCodex {
		b.WriteString(dimStyle.Render("  Codex OAuth needs a browser (open-grok login --codex).") + "\n")
		b.WriteString(dimStyle.Render("  Paste an API key here to store OPENAI_API_KEY in codex-auth.json.") + "\n\n")
	} else {
		b.WriteString(dimStyle.Render("  Paste an API key. It is written owner-only under ~/.opengrok.") + "\n\n")
	}
	shown := strings.Repeat("•", len(m.keyBuf))
	if shown == "" {
		shown = dimStyle.Render("(hidden)")
	}
	b.WriteString("  key: " + shown + "\n\n")
	b.WriteString(dimStyle.Render("  enter save  ·  esc back  ·  never commit this key"))
	return b.String()
}

func (m *model) handleLoginKey(msg tea.KeyMsg) tea.Cmd {
	if m.loginP == "" {
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.items) {
				m.cursor++
			}
		case "esc":
			m.screen = screenWelcome
			m.refreshWelcome()
		case "enter":
			if m.cursor < len(m.items) {
				p, ok := types.ParseProvider(m.items[m.cursor].Key)
				if ok {
					m.loginP = p
					m.keyBuf = ""
				}
			}
		}
		return nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.loginP = ""
		m.keyBuf = ""
		return nil
	case tea.KeyEnter:
		key := strings.TrimSpace(m.keyBuf)
		if key == "" {
			return nil
		}
		if err := auth.SaveAPIKey(m.loginP, key, m.loginKimiCode); err != nil {
			m.status = err.Error()
			return nil
		}
		m.keyBuf = ""
		m.status = "saved " + m.loginP.DisplayName() + " credentials"
		if m.sess != nil {
			m.screen = screenChat
			m.appendBlock(block{kind: blockSystem, body: "signed in to " + m.loginP.DisplayName() + " — credentials under ~/.opengrok, never ~/.grok"})
			m.refreshVP()
			return nil
		}
		m.screen = screenWelcome
		m.refreshWelcome()
		return nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.keyBuf) > 0 {
			m.keyBuf = m.keyBuf[:len(m.keyBuf)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.keyBuf += string(msg.Runes)
	}
	return nil
}

func (m *model) confirmDelete(id string) {
	sess, err := agent.LoadSession(m.opts.CWD, id)
	if err != nil {
		return
	}
	_ = agent.DeleteSession(sess)
	if m.sess != nil && m.sess.ID == id {
		m.sess = nil
		m.screen = screenWelcome
	}
	m.refreshWelcome()
}

func workspaceExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func pad(s string, n int) string {
	if lipgloss.Width(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-lipgloss.Width(s))
}
