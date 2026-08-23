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

func centerPlain(width int, s string) string {
	w := max(1, width)
	vis := lipgloss.Width(s)
	if vis >= w {
		return s
	}
	pad := (w - vis) / 2
	return strings.Repeat(" ", pad) + s
}

func (m *model) welcomeStackedLogo() string {
	lines := m.welcomeLogoLines()
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(centerPlain(m.width, line) + "\n")
	}
	b.WriteByte('\n')
	return b.String()
}

func (m *model) welcomeMenuLines() string {
	var b strings.Builder
	for i, it := range m.items {
		line := fmt.Sprintf("%s  %s", it.Label, dimStyle.Render(it.Detail))
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		}
		b.WriteString(centerPlain(m.width, line) + "\n")
	}
	return b.String()
}

func (m *model) renderWelcomeTrust() string {
	var b strings.Builder
	b.WriteString(m.welcomeStackedLogo())
	b.WriteString(centerPlain(m.width, claimStyle.Render(welcomeTrustQuestion)) + "\n")
	b.WriteString(centerPlain(m.width, titleStyle.Render(m.opts.CWD)) + "\n\n")
	b.WriteString(centerPlain(m.width, dimStyle.Render(welcomeTrustWarningLine1)) + "\n")
	b.WriteString(centerPlain(m.width, dimStyle.Render(welcomeTrustWarningLine2)) + "\n\n")
	b.WriteString(m.welcomeMenuLines())
	b.WriteString("\n" + centerPlain(m.width, dimStyle.Render("y proceed  ·  n quit")))
	return b.String()
}

func (m *model) renderWelcomeUnsigned() string {
	var b strings.Builder
	b.WriteString(m.welcomeStackedLogo())
	b.WriteString(centerPlain(m.width, claimStyle.Render(welcomeUnsignedPrompt)) + "\n\n")
	b.WriteString(m.welcomeMenuLines())
	b.WriteString("\n" + centerPlain(m.width, dimStyle.Render("c Codex  ·  x xAI  ·  q quit  ·  ↑↓ enter")))
	return b.String()
}

func (m *model) viewWelcome() string {
	var b strings.Builder
	loc := m.welcomeLocation()
	ver := "Entropy " + version.String()
	pad := max(1, m.width-lipgloss.Width(loc)-lipgloss.Width(ver))
	b.WriteString(dimStyle.Render(loc+strings.Repeat(" ", pad)+ver) + "\n\n")
	if m.trustPending {
		b.WriteString(m.renderWelcomeTrust())
		return b.String()
	}
	if m.unsignedIn() {
		b.WriteString(m.renderWelcomeUnsigned())
		return b.String()
	}
	useHero := m.width >= heroBoxMinWidth && m.height >= 22
	if useHero {
		b.WriteString(m.renderHeroBox() + "\n")
	} else {
		if logo := m.welcomeLogo(); logo != "" {
			b.WriteString(logo)
		}
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
			b.WriteString(errStyle.Render("  Not signed in — pick Login or run entropy login") + "\n\n")
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
		b.WriteString("\n")
		if m.showPrivacyBanner() {
			b.WriteString(privacyBanner() + "\n")
		}
		for _, bullet := range changelogBullets() {
			b.WriteString(dimStyle.Render("  • "+bullet) + "\n")
		}
	}
	if tip := m.welcomeTip(); tip != "" {
		b.WriteString("\n" + dimStyle.Render("  "+tip) + "\n")
	}
	b.WriteString("\n" + m.welcomePromptBox() + "\n")
	b.WriteString(dimStyle.Render("  type to start  ·  enter  ·  shift+tab new session  ·  " + quitChord() + " quit"))
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
	return m.openLogin(string(p))
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
		cmd := m.attachSession(sess)
		m.layoutChat()
		return cmd
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
		cmd := m.attachSession(sess)
		m.layoutChat()
		return cmd
	case key == "login":
		return m.openLogin("")
	case key == "settings":
		m.openSettings()
	case key == "quit":
		return tea.Quit
	}
	return nil
}

func (m *model) viewLogin() string {
	var b strings.Builder
	b.WriteString(headerBar.Width(max(20, m.width)).Render(" Sign in  ·  ~/.entropy only") + "\n\n")
	body := m.loginBody()
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("  "+quitChord()+" quit"))
	return b.String()
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
