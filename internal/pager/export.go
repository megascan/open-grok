package pager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/types"
)

func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func (m *model) conversationExportMarkdown() string {
	if m.sess == nil {
		return ""
	}
	has := false
	for _, msg := range m.sess.Messages {
		if (msg.Role == types.RoleUser || msg.Role == types.RoleAssistant) && strings.TrimSpace(msg.Content) != "" {
			has = true
			break
		}
	}
	if !has {
		for _, b := range m.blocks {
			if (b.kind == blockUser || b.kind == blockAgent) && strings.TrimSpace(b.body) != "" {
				has = true
				break
			}
		}
	}
	if !has {
		return ""
	}
	return m.sess.ExportMarkdown()
}

func clipboardStatsSuffix(s string) string {
	lines := strings.Count(s, "\n") + 1
	if strings.TrimSpace(s) == "" {
		lines = 0
	}
	return fmt.Sprintf(" (%d chars, %d lines)", utf8.RuneCountInString(s), lines)
}

func (m *model) exportConversation(path string) tea.Cmd {
	if m.sess == nil {
		m.appendSys("No active session to export")
		return nil
	}
	md := m.conversationExportMarkdown()
	if md == "" {
		m.appendSys("No conversation content to export")
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		cmd := m.copyText(md, true)
		stats := clipboardStatsSuffix(md)
		if strings.Contains(m.status, "unreachable") {
			m.appendSys("Clipboard unreachable — conversation written to " + m.copied + stats)
		} else {
			disp := m.copied
			if disp != "" {
				m.appendSys("Conversation copied to clipboard (also saved to " + disp + ")" + stats)
			} else {
				m.appendSys("Conversation copied to clipboard" + stats)
			}
		}
		return cmd
	}
	expanded := expandTilde(path)
	if dir := filepath.Dir(expanded); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.appendSys("Failed to create directory: " + err.Error())
			return nil
		}
	}
	if err := os.WriteFile(expanded, []byte(md), 0o600); err != nil {
		m.appendSys("Failed to write file: " + err.Error())
		return nil
	}
	m.appendSys("Conversation exported to " + expanded)
	return nil
}

func (m *model) openTranscriptPager() tea.Cmd {
	if m.sess == nil {
		m.appendSys("No active session to view")
		return nil
	}
	md := m.conversationExportMarkdown()
	if md == "" {
		m.appendSys("No conversation transcript to view yet")
		return nil
	}
	path := filepath.Join(os.TempDir(), "entropy-transcript-"+m.sess.ID+".md")
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		m.appendSys("Failed to write transcript: " + err.Error())
		return nil
	}
	pager := strings.TrimSpace(os.Getenv("PAGER"))
	if pager == "" {
		pager = "less"
	}
	parts := strings.Fields(pager)
	args := append(append([]string{}, parts[1:]...), path)
	c := exec.Command(parts[0], args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return nil
	})
}
