package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/workspace"
)

// handleNew is /new. In a git repo on the agent screen, Ask mode opens the
// worktree question; Always creates a worktree; Never (default) stays in cwd.
// Welcome and other screens skip the modal, matching dispatch_new_session.
func (m *model) handleNew() tea.Cmd {
	inGit := workspace.HasGitAncestor(m.opts.CWD)
	if inGit && m.screen == screenChat {
		switch m.newSessionWorktreeMode() {
		case "always":
			return m.startNewSession(true)
		case "ask":
			return m.openNewSessionQuestion()
		}
	}
	return m.startNewSession(false)
}

func (m *model) newSessionWorktreeMode() string {
	if m.cfg.Prefs == nil {
		return "never"
	}
	v := strings.ToLower(strings.TrimSpace(m.cfg.Prefs["new_session_worktree_mode"]))
	switch v {
	case "always", "ask", "never":
		return v
	}
	return "never"
}

func (m *model) openNewSessionQuestion() tea.Cmd {
	if m.question != nil {
		m.appendSys("Finish answering the current question first")
		return nil
	}
	m.question = &qAskMsg{
		Question: "Start the new session in an isolated git worktree?",
		Options:  []string{"Yes", "No", "Always worktree", "Never worktree"},
		Kind:     "new-worktree",
	}
	m.qCursor = 0
	m.qFree = ""
	m.qTyping = false
	m.overlay = overlayQuestion
	return nil
}

func (m *model) resolveNewWorktree(ans string) tea.Cmd {
	label := strings.TrimSpace(ans)
	worktree := false
	persist := ""
	switch strings.ToLower(label) {
	case "yes":
		worktree = true
	case "no":
		worktree = false
	case "always worktree":
		worktree = true
		persist = "always"
	case "never worktree":
		worktree = false
		persist = "never"
	case "":
		return nil
	default:
		return nil
	}
	if persist != "" {
		if m.cfg.Prefs == nil {
			m.cfg.Prefs = config.Prefs{}
		}
		m.cfg.Prefs["new_session_worktree_mode"] = persist
		_ = config.Save(m.cfg)
	}
	return m.startNewSession(worktree)
}

func (m *model) startNewSession(worktree bool) tea.Cmd {
	cwd := m.opts.CWD
	if worktree {
		dest, err := m.addGitWorktree("")
		if err != nil {
			m.appendSys(err.Error())
			return nil
		}
		cwd = dest
		m.opts.CWD = dest
		m.dashCWD = dest
	}
	sess, err := agent.NewSession(cwd, m.opts.Model, m.permMode == permAlways)
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	cmd := m.attachSession(sess)
	m.layoutChat()
	if worktree {
		m.appendSys("Creating worktree\u2026")
	}
	return cmd
}
