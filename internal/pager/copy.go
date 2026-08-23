package pager

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/megascan/open-grok/internal/config"
)

func (m *model) copyBlock(idx int) {
	if len(m.blocks) == 0 {
		return
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
		return
	}
	m.copyText(m.blocks[idx].body)
}

func (m *model) copyNthAgent(n int) {
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
		return
	}
	m.copyText(agents[len(agents)-n])
}

func (m *model) copyText(s string) {
	backup := filepath.Join(config.Home(), "last-copy.txt")
	if v := config.FirstEnv("ENTROPY_COPY_FILE", "GROK_COPY_FILE"); v != "" {
		backup = v
	}
	_ = os.WriteFile(backup, []byte(s), 0o600)
	if err := clipboard.WriteAll(s); err != nil {
		m.copied = backup
		m.appendSys("clipboard unreachable — wrote " + backup)
		return
	}
	m.copied = backup
	m.status = "Copied!"
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
