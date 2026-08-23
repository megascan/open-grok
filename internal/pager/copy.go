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
	return m.copyText(m.blocks[idx].body, true)
}

func (m *model) copyNthAgent(n int) tea.Cmd {
	var agents []string
	for _, b := range m.blocks {
		if b.kind == blockAgent && strings.TrimSpace(b.body) != "" {
			agents = append(agents, b.body)
		}
	}
	if n <= 0 {
		n = 1
	}
	if n > len(agents) {
		n = len(agents)
	}
	if n == 0 {
		return nil
	}
	return m.copyText(agents[len(agents)-n], true)
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
	case runtime.GOOS == "linux":
		return "Copied via OSC 52."
	default:
		return "Copied!" + stats
	}
}

func osc52(s string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
}

func parseCopyArg(arg string) (n int, path string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 1, ""
	}
	fields := strings.Fields(arg)
	if n, err := strconv.Atoi(fields[0]); err == nil {
		path = strings.TrimSpace(strings.TrimPrefix(arg, fields[0]))
		return n, path
	}
	return 1, arg
}
