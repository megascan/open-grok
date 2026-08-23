package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/workspace"
)

type forkArgs struct {
	worktreeOverride *bool
	directive        string
}

// parseForkArgs recognises `--worktree` / `--no-worktree` at the start of the
// argument string. Unknown `--foo` tokens begin the directive, matching
// slash/commands/fork.rs. `--at` is rejected in this version.
func parseForkArgs(args string) (forkArgs, error) {
	var out forkArgs
	rest := strings.TrimLeft(args, " \t")
	for rest != "" {
		flag, after := splitWS(rest)
		switch flag {
		case "--worktree":
			if out.worktreeOverride != nil && !*out.worktreeOverride {
				return forkArgs{}, errString("--worktree and --no-worktree are mutually exclusive")
			}
			if out.worktreeOverride != nil && *out.worktreeOverride {
				return forkArgs{}, errString("--worktree specified twice")
			}
			v := true
			out.worktreeOverride = &v
			rest = strings.TrimLeft(after, " \t")
		case "--no-worktree":
			if out.worktreeOverride != nil && *out.worktreeOverride {
				return forkArgs{}, errString("--worktree and --no-worktree are mutually exclusive")
			}
			if out.worktreeOverride != nil && !*out.worktreeOverride {
				return forkArgs{}, errString("--no-worktree specified twice")
			}
			v := false
			out.worktreeOverride = &v
			rest = strings.TrimLeft(after, " \t")
		case "--at":
			return forkArgs{}, errString("--at is not supported in this version")
		default:
			out.directive = rest
			return out, nil
		}
	}
	return out, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func splitWS(s string) (token, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

func (m *model) handleFork(arg string) tea.Cmd {
	if m.sess == nil {
		m.appendSys("No active session")
		return nil
	}
	args, err := parseForkArgs(arg)
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	inGit := workspace.HasGitAncestor(m.opts.CWD)
	if args.worktreeOverride != nil {
		if *args.worktreeOverride && !inGit {
			m.appendSys("Cannot create worktree: not in a git repository")
			return nil
		}
		return m.executeFork(*args.worktreeOverride, args.directive)
	}
	if !inGit {
		return m.executeFork(false, args.directive)
	}
	switch strings.ToLower(strings.TrimSpace(m.forkWorktreeMode())) {
	case "always":
		return m.executeFork(true, args.directive)
	case "never":
		return m.executeFork(false, args.directive)
	}
	return m.openForkQuestion(args.directive)
}

func (m *model) forkWorktreeMode() string {
	if m.cfg.Prefs == nil {
		return ""
	}
	return m.cfg.Prefs["fork_worktree_mode"]
}

func (m *model) openForkQuestion(directive string) tea.Cmd {
	if m.question != nil {
		m.appendSys("Finish answering the current question first")
		return nil
	}
	m.question = &qAskMsg{
		Question:  "Run this fork in an isolated git worktree?",
		Options:   []string{"Yes", "No", "Always worktree", "Never worktree"},
		Kind:      "fork-worktree",
		Directive: directive,
	}
	m.qCursor = 0
	m.qFree = ""
	m.qTyping = false
	m.overlay = overlayQuestion
	m.qSel = nil
	m.ensureQuestionState()
	return nil
}

func (m *model) resolveForkWorktree(ans, directive string) tea.Cmd {
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
		m.cfg.Prefs["fork_worktree_mode"] = persist
		_ = config.Save(m.cfg)
	}
	return m.executeFork(worktree, directive)
}

func (m *model) executeFork(worktree bool, directive string) tea.Cmd {
	if m.sess == nil {
		m.appendSys("No active session")
		return nil
	}
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
	dst, err := agent.ForkSessionInto(m.sess, cwd)
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	marker := "Forked"
	if directive != "" {
		marker = "Forked: " + directive
	}
	m.appendSys(marker)
	cmd := m.attachSession(dst)
	m.layoutChat()
	if worktree {
		m.appendSys("Creating worktree\u2026")
	}
	m.appendSys("forked to " + dst.ID)
	if directive != "" {
		return tea.Batch(cmd, m.submit(directive))
	}
	return cmd
}
