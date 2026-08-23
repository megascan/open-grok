package pager

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

const heroBoxMinWidth = 90

func vscodeFamily() bool {
	p := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	switch p {
	case "vscode", "cursor", "zed", "vscodeinsiders":
		return true
	}
	return os.Getenv("VSCODE_INJECTION") != "" || os.Getenv("VSCODE_GIT_IPC_HANDLE") != ""
}

func quitChord() string {
	if vscodeFamily() {
		return "ctrl+d"
	}
	return "ctrl+q"
}

var welcomeTips = []string{
	"/btw asks a side question without interrupting",
	"ctrl+. or ctrl+x opens the shortcuts cheatsheet",
	"shift+tab starts a new session from welcome",
	"/rewind restores a previous turn",
	"/dashboard lists live sessions — ctrl+\\ from chat",
	"g opens the goal detail overlay when a goal is active",
}

func (m *model) showTips() bool {
	return m.cfg.PrefBool("show_tips", m.cfg.UI.ShowTips, true)
}

func (m *model) welcomeTip() string {
	if !m.showTips() || m.unsignedIn() || m.trustPending {
		return ""
	}
	if len(welcomeTips) == 0 {
		return ""
	}
	i := int(time.Now().Unix()/30) % len(welcomeTips)
	return welcomeTips[i]
}

func (m *model) refreshGitLoc() {
	m.gitBranch = ""
	m.gitWorktree = false
	cwd := m.opts.CWD
	if cwd == "" {
		return
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		m.gitBranch = strings.TrimSpace(string(out))
	}
	wt, err := exec.Command("git", "-C", cwd, "rev-parse", "--git-common-dir").Output()
	if err == nil {
		common := strings.TrimSpace(string(wt))
		gitDir, _ := exec.Command("git", "-C", cwd, "rev-parse", "--git-dir").Output()
		m.gitWorktree = common != "" && strings.TrimSpace(string(gitDir)) != common
	}
}

func (m *model) welcomeLocation() string {
	cwd := m.opts.CWD
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(cwd, home) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}
	var b strings.Builder
	if m.gitBranch != "" {
		b.WriteString("⎇ " + m.gitBranch + "  ")
	}
	if m.gitWorktree {
		b.WriteString("worktree  ")
	}
	b.WriteString(cwd)
	return b.String()
}

func (m *model) welcomeLogoLines() []string {
	src := ""
	switch {
	case m.height >= 26:
		src = strings.TrimRight(logoFull, "\n")
	case m.height >= 22:
		src = strings.TrimRight(logoSmall, "\n")
	default:
		return nil
	}
	var lines []string
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, titleStyle.Render(line))
	}
	return lines
}

func (m *model) renderHeroBox() string {
	logo := m.welcomeLogoLines()
	var right []string
	right = append(right, titleStyle.Render("Entropy "+version.String()))
	if m.trustPending || m.unsignedIn() {
		// keep compact
	} else {
		right = append(right, dimStyle.Render("Thanks for trying Entropy, give feedback with /feedback!"))
	}
	right = append(right, "")
	st := authStatusLine(m)
	if st != "" {
		right = append(right, st)
		right = append(right, "")
	}
	if m.showPrivacyBanner() && !m.unsignedIn() && !m.trustPending {
		right = append(right, strings.Split(strings.TrimRight(privacyBanner(), "\n"), "\n")...)
		right = append(right, "")
	}
	for i, it := range m.items {
		line := fmt.Sprintf("%s  %s", it.Label, dimStyle.Render(it.Detail))
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		} else {
			line = "  " + line
		}
		right = append(right, line)
	}
	if !m.unsignedIn() && !m.trustPending {
		right = append(right, "")
		for _, bullet := range changelogBullets() {
			right = append(right, dimStyle.Render("• "+bullet))
		}
	}
	for len(logo) < len(right) {
		logo = append(logo, "")
	}
	for len(right) < len(logo) {
		right = append(right, "")
	}
	var rows []string
	for i := range logo {
		left := logo[i]
		if lipgloss.Width(left) < 28 && left != "" {
			left += strings.Repeat(" ", 28-lipgloss.Width(left))
		}
		if left == "" {
			left = strings.Repeat(" ", 28)
		}
		rows = append(rows, left+"   "+right[i])
	}
	inner := strings.Join(rows, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("51")).
		Padding(1, 2).
		Width(min(120, max(40, m.width-6)))
	return box.Render(inner)
}

func authStatusLine(m *model) string {
	if m.trustPending {
		return claimStyle.Render("Do you trust the contents of this directory?")
	}
	if m.unsignedIn() {
		return claimStyle.Render("Choose the account that will power this session.")
	}
	st := auth.Status()
	var signed []string
	for _, p := range types.AllProviders {
		if _, ok := st[p]; ok {
			signed = append(signed, p.DisplayName())
		}
	}
	if len(signed) == 0 {
		return errStyle.Render("Not signed in — pick Login or run entropy login")
	}
	return okStyle.Render("Signed in: " + strings.Join(signed, ", "))
}

func (m *model) welcomePromptBox() string {
	w := max(24, m.width-4)
	ph := "Type a message..."
	return inputBox.Width(w).Render(dimStyle.Render("▎ " + ph))
}
