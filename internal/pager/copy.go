package pager

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
)

func (m *model) copyBlock(idx int) tea.Cmd {
	if len(m.blocks) == 0 {
		return nil
	}
	if idx < 0 {
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].kind == blockAgent && strings.TrimSpace(m.blocks[i].body) != "" {
				idx = i
				break
			}
		}
	}
	if idx < 0 || idx >= len(m.blocks) {
		return nil
	}
	bl := m.blocks[idx]
	text := bl.body
	switch bl.kind {
	case blockSession:
		text = bl.event.message()
	case blockSubagent, blockSwarm, blockBgTask, blockWorkflow:
		text = strings.TrimSpace(stripANSI(m.renderBlock(bl, 80)))
	}
	return m.copyText(text, true)
}

func (m *model) copyNthAgent(n int) tea.Cmd {
	body, err := m.nthAssistantBody(n)
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	return m.copyText(body, true)
}

func (m *model) copyAssistant(n int, path string) tea.Cmd {
	body, err := m.nthAssistantBody(n)
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	if path != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			m.appendSys("Failed to write file: " + err.Error())
			return nil
		}
		lines := strings.Count(body, "\n") + 1
		if strings.TrimSpace(body) == "" {
			lines = 0
		}
		m.appendSys(fmt.Sprintf("Copied to %s (%d chars, %d lines)", path, utf8.RuneCountInString(body), lines))
		return nil
	}
	return m.copyText(body, true)
}

func (m *model) nthAssistantBody(n int) (string, error) {
	var agents []string
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockAgent && strings.TrimSpace(m.blocks[i].body) != "" {
			agents = append(agents, m.blocks[i].body)
		}
	}
	if len(agents) == 0 {
		return "", errString("No assistant messages to copy")
	}
	if n > len(agents) {
		word := "messages"
		if len(agents) == 1 {
			word = "message"
		}
		return "", errString(fmt.Sprintf("Only %d assistant %s available to copy", len(agents), word))
	}
	text := agents[n-1]
	if strings.TrimSpace(text) == "" {
		return "", errString("Assistant message is empty")
	}
	return text, nil
}

func (m *model) copyText(s string, withStats bool) tea.Cmd {
	backup := filepath.Join(config.Home(), "last-copy.txt")
	if v := config.FirstEnv("ENTROPY_COPY_FILE", "GROK_COPY_FILE"); v != "" {
		backup = v
	}
	_ = os.WriteFile(backup, []byte(s), 0o600)
	m.copied = backup
	nativeOK := clipboard.WriteAll(s) == nil
	oscOff := config.FirstEnv("ENTROPY_CLIPBOARD_NO_OSC52", "GROK_CLIPBOARD_NO_OSC52") != ""
	toast := copyToast(nativeOK, oscOff, backup, s, withStats)
	m.status = toast
	if oscOff {
		return nil
	}
	seq := osc52(s)
	return func() tea.Msg {
		_, _ = fmt.Fprint(os.Stdout, seq)
		return nil
	}
}

func copyToast(nativeOK, oscOff bool, backup, s string, withStats bool) string {
	stats := ""
	if withStats {
		lines := strings.Count(s, "\n") + 1
		if strings.TrimSpace(s) == "" {
			lines = 0
		}
		stats = fmt.Sprintf(" (%d chars, %d lines)", utf8.RuneCountInString(s), lines)
	}
	home := config.Home()
	disp := backup
	if strings.HasPrefix(backup, home) {
		disp = "~/.entropy" + strings.TrimPrefix(backup, home)
	}
	switch {
	case !nativeOK:
		return "Clipboard unreachable — wrote " + disp
	case oscOff:
		return "Copied!" + stats
	case os.Getenv("TMUX") != "":
		return "Copied via OSC 52. If paste fails in tmux, set allow-passthrough on." + stats
	case vscodeFamily():
		return "Copied via OSC 52. VS Code SSH: enable the remote terminal clipboard." + stats
	case runtime.GOOS == "linux":
		return "Copied via OSC 52." + stats
	default:
		return "Copied!" + stats
	}
}

func osc52(s string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
}

func parseCopyArg(arg string) (n int, path string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 1, "", nil
	}
	var first, rest string
	if i := strings.IndexFunc(arg, unicodeIsSpace); i >= 0 {
		first, rest = arg[:i], strings.TrimSpace(arg[i+1:])
	} else {
		first = arg
	}
	if n, e := strconv.ParseUint(first, 10, 32); e == nil {
		if n == 0 {
			return 0, "", errString("Usage: /copy [N] [file] where N is 1 (latest), 2, 3, ...")
		}
		return int(n), rest, nil
	}
	return 1, arg, nil
}

func unicodeIsSpace(r rune) bool {
	return r == ' ' || r == '\t'
}
