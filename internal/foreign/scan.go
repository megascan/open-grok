// Package foreign lists recent Claude Code and Codex CLI sessions for the
// resume picker. Metadata only; never opens SQLite or writes ~/.grok.
package foreign

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxPerTool = 50
const maxAge = 30 * 24 * time.Hour

// Session is one picker row from another coding agent.
type Session struct {
	Tool      string // claude | codex
	ID        string
	Title     string
	CWD       string
	UpdatedAt time.Time
}

// ScanRecent returns bounded Claude and Codex session summaries for cwd.
func ScanRecent(cwd string) []Session {
	var out []Session
	out = append(out, scanClaude(cwd)...)
	out = append(out, scanCodex(cwd)...)
	if len(out) > maxPerTool*2 {
		out = out[:maxPerTool*2]
	}
	return out
}

func scanClaude(cwd string) []Session {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	projects := filepath.Join(home, ".claude", "projects")
	dir := filepath.Join(projects, sanitizePath(cwd))
	return scanJSONLDir(dir, "claude", cwd)
}

func scanCodex(cwd string) []Session {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".codex", "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Session
	n := 0
	for i := len(entries) - 1; i >= 0 && n < 32; i-- {
		e := entries[i]
		if !e.IsDir() {
			continue
		}
		n++
		out = append(out, scanJSONLDir(filepath.Join(root, e.Name()), "codex", cwd)...)
		if len(out) >= maxPerTool {
			break
		}
	}
	return out
}

func scanJSONLDir(dir, tool, cwd string) []Session {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-maxAge)
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		st, err := os.Stat(path)
		if err != nil || st.ModTime().Before(cutoff) {
			continue
		}
		s := Session{
			Tool:      tool,
			ID:        strings.TrimSuffix(e.Name(), ".jsonl"),
			Title:     e.Name(),
			CWD:       cwd,
			UpdatedAt: st.ModTime(),
		}
		if title, sessCWD := peekJSONL(path); title != "" {
			s.Title = title
			if sessCWD != "" {
				s.CWD = sessCWD
			}
		}
		out = append(out, s)
		if len(out) >= maxPerTool {
			break
		}
	}
	return out
}

func peekJSONL(path string) (title, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for i := 0; i < 12 && sc.Scan(); i++ {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		if cwd == "" {
			if v, ok := row["cwd"].(string); ok {
				cwd = v
			}
		}
		if title == "" {
			if v, ok := row["title"].(string); ok {
				title = v
			}
		}
		if title == "" {
			if msg, ok := row["message"].(map[string]any); ok {
				if role, _ := msg["role"].(string); role == "user" {
					if c, ok := msg["content"].(string); ok {
						title = truncate(c, 80)
					}
				}
			}
		}
	}
	return title, cwd
}

func sanitizePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 200 {
			break
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
