package pager

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/workspace"
)

func (m *model) openWorktreeDialog() {
	if !workspace.HasGitAncestor(m.opts.CWD) {
		m.status = "New worktree needs a git repository"
		return
	}
	m.overlay = overlayWorktree
	m.worktreeLabel = ""
}

func (m *model) renderWorktreeDialog(w int) string {
	body := titleStyle.Render("New worktree") + "\n\n" +
		dimStyle.Render("Empty label uses a timestamp. Entropy then opens a session in the new tree.") + "\n\n" +
		dimStyle.Render("Name (optional): ") + m.worktreeLabel + "█\n\n" +
		dimStyle.Render("enter create  ·  esc cancel")
	return modalBox.Width(w).Render(body)
}

func (m *model) handleWorktreeKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.overlay = overlayNone
		m.worktreeLabel = ""
		return nil
	case tea.KeyEnter:
		label := strings.TrimSpace(m.worktreeLabel)
		m.overlay = overlayNone
		m.worktreeLabel = ""
		return m.createWorktreeSession(label)
	case tea.KeyBackspace:
		if len(m.worktreeLabel) > 0 {
			m.worktreeLabel = m.worktreeLabel[:len(m.worktreeLabel)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		if len(m.worktreeLabel) < 100 {
			m.worktreeLabel += string(msg.Runes)
		}
	}
	return nil
}

func (m *model) createWorktreeSession(label string) tea.Cmd {
	root, ok := workspace.GitRoot(m.opts.CWD)
	if !ok {
		m.status = "not a git repository"
		return nil
	}
	safe := workspace.SanitizeWorktreeLabel(label)
	if safe == "" {
		safe = time.Now().Format("20060102-150405")
	}
	dest := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+safe)
	cmd := exec.Command("git", "-C", root, "worktree", "add", dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.status = fmt.Sprintf("git worktree add: %v\n%s", err, strings.TrimSpace(string(out)))
		return nil
	}
	m.opts.CWD = dest
	m.dashCWD = dest
	sess, err := agent.NewSession(dest, m.opts.Model, m.permMode == permAlways)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	sess.Title = "worktree " + safe
	_ = sess.Persist()
	m.attachSession(sess)
	m.layoutChat()
	m.appendSys("opened worktree " + dest)
	return nil
}
