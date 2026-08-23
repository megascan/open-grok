package pager

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
	"github.com/megascan/open-grok/internal/workspace"
)

func (m *model) unsignedIn() bool {
	st := auth.Status()
	_, xai := st[types.ProviderXAI]
	_, codex := st[types.ProviderCodex]
	return !xai && !codex
}

func (m *model) refreshWelcome() {
	m.refreshGitLoc()
	m.items = nil
	if m.unsignedIn() && !m.trustPending {
		m.items = []listItem{
			{Key: "codex", Label: "c  Continue with ChatGPT Codex", Detail: "Codex OAuth or API key"},
			{Key: "xai", Label: "x  Continue with xAI Grok", Detail: "xAI API key"},
			{Key: "quit", Label: "q  Quit", Detail: "leave Entropy"},
		}
		if m.cursor >= len(m.items) {
			m.cursor = 0
		}
		return
	}
	if m.trustPending {
		m.items = []listItem{
			{Key: "trust-yes", Label: "y  Yes, proceed", Detail: m.opts.CWD},
			{Key: "trust-no", Label: "n  No, quit", Detail: "leave without trusting this folder"},
		}
		if m.cursor >= len(m.items) {
			m.cursor = 0
		}
		return
	}
	if m.hasClaudeImport() {
		m.items = append(m.items, listItem{Key: "import-claude", Label: "Import Claude settings", Detail: "ctrl+i   [x]"})
	}
	m.items = append(m.items,
		listItem{Key: "worktree", Label: "New worktree", Detail: "ctrl+w"},
		listItem{Key: "resume", Label: "Resume session", Detail: "ctrl+s"},
		listItem{Key: "changelog", Label: "Changelog", Detail: ""},
		listItem{Key: "quit", Label: "Quit", Detail: quitChord()},
	)
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) viewWelcome() string {
	var b strings.Builder
	loc := m.welcomeLocation()
	ver := "Entropy " + version.String()
	pad := max(1, m.width-lipgloss.Width(loc)-lipgloss.Width(ver))
	b.WriteString(dimStyle.Render(loc+strings.Repeat(" ", pad)+ver) + "\n\n")
	useHero := m.width >= heroBoxMinWidth && m.height >= 22 && !m.trustPending
	if useHero {
		b.WriteString(m.renderHeroBox() + "\n")
	} else {
		if logo := m.welcomeLogo(); logo != "" {
			b.WriteString(logo)
		}
		b.WriteString(titleStyle.Render("  Welcome") + "\n")
		b.WriteString(dimStyle.Render("  State: "+config.Home()+"   never ~/.grok") + "\n")
		b.WriteString(dimStyle.Render("  Model: "+m.opts.Model.Name+" via "+m.opts.Model.Provider.DisplayName()) + "\n\n")
		if m.trustPending {
			b.WriteString(claimStyle.Render("  Do you trust the contents of this directory?") + "\n")
			b.WriteString(titleStyle.Render("  "+m.opts.CWD) + "\n\n")
			b.WriteString(dimStyle.Render("  Entropy may run or modify contents in this directory,") + "\n")
			b.WriteString(dimStyle.Render("  posing security risks.") + "\n\n")
		} else if m.unsignedIn() {
			b.WriteString(claimStyle.Render("  Choose the account that will power this session.") + "\n\n")
		} else {
			st := auth.Status()
			var signed []string
			for _, p := range types.AllProviders {
				if _, ok := st[p]; ok {
					signed = append(signed, p.DisplayName())
				}
			}
			if len(signed) == 0 {
				b.WriteString(errStyle.Render("  Not signed in — pick Login or run entropy login") + "\n\n")
			} else {
				b.WriteString(okStyle.Render("  Signed in: "+strings.Join(signed, ", ")) + "\n\n")
			}
		}
		for i, it := range m.items {
			line := fmt.Sprintf("  %s  %s", it.Label, dimStyle.Render(it.Detail))
			if i == m.cursor {
				line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
			}
			b.WriteString(line + "\n")
		}
		if !m.unsignedIn() && !m.trustPending {
			b.WriteString("\n")
			if m.showPrivacyBanner() {
				b.WriteString(privacyBanner() + "\n")
			}
			for _, bullet := range changelogBullets() {
				b.WriteString(dimStyle.Render("  • "+bullet) + "\n")
			}
		}
	}
	if !m.unsignedIn() && !m.trustPending {
		if tip := m.welcomeTip(); tip != "" {
			b.WriteString("\n" + dimStyle.Render("  "+tip) + "\n")
		}
		b.WriteString("\n" + m.welcomePromptBox() + "\n")
		b.WriteString(dimStyle.Render("  type to start  ·  enter  ·  shift+tab new session  ·  " + quitChord() + " quit"))
	}
	hint := "  ↑↓ move  ·  enter  ·  q quit"
	if m.unsignedIn() && !m.trustPending {
		hint = "  c Codex  ·  x xAI  ·  q quit  ·  ↑↓ enter"
	}
	if m.trustPending {
		hint = "  y proceed  ·  n quit"
	}
	if m.unsignedIn() || m.trustPending {
		b.WriteString("\n" + dimStyle.Render(hint))
	}
	return b.String()
}

func (m *model) handleWelcomeKey(msg tea.KeyMsg) tea.Cmd {
	if m.trustPending {
		switch msg.String() {
		case "y", "enter":
			if msg.String() == "enter" && m.cursor < len(m.items) && m.items[m.cursor].Key == "trust-no" {
				return tea.Quit
			}
			return m.acceptFolderTrust()
		case "n", "q", "ctrl+c", "ctrl+d":
			return tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.items) {
				m.cursor++
			}
		}
		return nil
	}
	if m.unsignedIn() {
		switch msg.String() {
		case "c":
			return m.chooseStartup(types.ProviderCodex)
		case "x":
			return m.chooseStartup(types.ProviderXAI)
		case "q", "ctrl+c", "ctrl+d":
			return tea.Quit
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
		case "enter":
			if m.cursor >= len(m.items) {
				return nil
			}
			return m.pickWelcome(m.items[m.cursor].Key)
		}
		return nil
	}
	s := msg.String()
	switch s {
	case "ctrl+x", "ctrl+.":
		m.openShortcuts()
		return nil
	case "ctrl+s":
		m.openResume()
		return nil
	case "ctrl+w":
		m.openWorktreeDialog()
		return nil
	case "ctrl+i":
		if m.hasClaudeImport() {
			m.openImportClaude()
		}
		return nil
	case "ctrl+I":
		if m.hasClaudeImport() {
			m.dismissClaudeImport()
		}
		return nil
	case "ctrl+p":
		if m.showPrivacyBanner() {
			m.ackPrivacyBanner(false)
			m.refreshWelcome()
		}
		return nil
	case "shift+tab":
		return m.pickWelcome("new")
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
	case "q", "ctrl+q", "ctrl+c", "ctrl+d":
		return tea.Quit
	case "enter":
		if m.cursor >= len(m.items) {
			return m.pickWelcome("new")
		}
		return m.pickWelcome(m.items[m.cursor].Key)
	}
	if msg.Type == tea.KeyRunes && s != "k" && s != "j" && s != "q" {
		cmd := m.pickWelcome("new")
		m.ta.SetValue(string(msg.Runes))
		m.ta.Focus()
		return cmd
	}
	return nil
}

func (m *model) acceptFolderTrust() tea.Cmd {
	_ = workspace.GrantFolderTrust(m.opts.CWD)
	m.trustPending = false
	if m.opts.Dashboard {
		m.openDashboard()
		return nil
	}
	if m.opts.Resume != "" || m.opts.Continue {
		if err := m.openSession(m.opts); err != nil {
			m.status = err.Error()
			m.refreshWelcome()
			return nil
		}
		m.layoutChat()
		return nil
	}
	m.refreshWelcome()
	return nil
}

func (m *model) chooseStartup(p types.Provider) tea.Cmd {
	for _, mod := range catalog.All() {
		if mod.Provider == p {
			m.opts.Model = mod
			break
		}
	}
	m.openLogin(string(p))
	return nil
}

func (m *model) pickWelcome(key string) tea.Cmd {
	switch {
	case key == "codex":
		return m.chooseStartup(types.ProviderCodex)
	case key == "xai":
		return m.chooseStartup(types.ProviderXAI)
	case key == "new":
		sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, m.permMode == permAlways)
		if err != nil {
			m.status = err.Error()
			return nil
		}
		m.attachSession(sess)
		m.layoutChat()
	case key == "resume":
		m.openResume()
	case key == "worktree":
		m.openWorktreeDialog()
	case key == "import-claude":
		m.openImportClaude()
	case key == "changelog":
		m.openReleaseNotes()
	case key == "trust-yes":
		return m.acceptFolderTrust()
	case key == "trust-no":
		return tea.Quit
	case strings.HasPrefix(key, "resume:"):
		id := strings.TrimPrefix(key, "resume:")
		sess, err := agent.FindSession(id)
		if err != nil {
			sess, err = agent.LoadSession(m.opts.CWD, id)
		}
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
	b.WriteString(headerBar.Width(max(20, m.width)).Render(" Sign in  ·  ~/.entropy only") + "\n\n")
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
		b.WriteString(dimStyle.Render("  Codex OAuth needs a browser (entropy login --codex).") + "\n")
		b.WriteString(dimStyle.Render("  Paste an API key here to store OPENAI_API_KEY in codex-auth.json.") + "\n\n")
	} else {
		b.WriteString(dimStyle.Render("  Paste an API key. It is written owner-only under ~/.entropy.") + "\n\n")
	}
	shown := strings.Repeat("•", len(m.keyBuf))
	if shown == "" {
		shown = dimStyle.Render("(hidden)")
	}
	b.WriteString("  key: " + shown + "\n\n")
	b.WriteString(dimStyle.Render("  enter save  ·  esc back  ·  " + quitChord() + " quit  ·  never commit this key"))
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
			m.appendBlock(block{kind: blockSystem, body: "signed in to " + m.loginP.DisplayName() + " — credentials under ~/.entropy, never ~/.grok"})
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
	sess, err := agent.FindSession(id)
	if err != nil {
		sess, err = agent.LoadSession(m.opts.CWD, id)
	}
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
