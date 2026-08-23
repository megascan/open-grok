package pager

import (
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// atToken is the open `@path` completion under the cursor (end of the draft).
type atToken struct {
	Query  string
	Start  int // byte index of '@'
	End    int
	Hidden bool // `@!` includes dotfiles
}

func parseAtToken(s string) (atToken, bool) {
	i := strings.LastIndexByte(s, '@')
	if i < 0 {
		return atToken{}, false
	}
	if i > 0 {
		r := rune(s[i-1])
		if !unicode.IsSpace(r) && r != '\n' && r != '\t' {
			return atToken{}, false
		}
	}
	rest := s[i+1:]
	if strings.ContainsAny(rest, " \t\n") {
		return atToken{}, false
	}
	hidden := strings.HasPrefix(rest, "!")
	q := rest
	if hidden {
		q = strings.TrimPrefix(q, "!")
	}
	return atToken{Query: q, Start: i, End: len(s), Hidden: hidden}, true
}

func acceptAtPath(draft string, tok atToken, path string, isDir, tab bool) string {
	prefix := draft[:tok.Start]
	ins := path
	if isDir {
		if tab {
			if !strings.HasSuffix(ins, "/") {
				ins += "/"
			}
			if tok.End == len(draft) {
				ins += " "
			}
		}
		// Right-arrow drill (tab=false): no trailing slash, keep @-context.
	} else {
		ins += " "
	}
	marker := "@"
	if tok.Hidden {
		marker = "@!"
	}
	if isDir && !tab {
		return prefix + marker + ins
	}
	return prefix + marker + ins
}

func listWorkspaceFiles(root, query string, hidden bool) []listItem {
	q := strings.ToLower(query)
	var hits []listItem
	skip := map[string]bool{
		".git": true, "node_modules": true, "target": true, "dist": true,
		"vendor": true, ".entropy": true, ".opengrok": true,
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || len(hits) >= 80 {
			if len(hits) >= 80 {
				return fs.SkipAll
			}
			return nil
		}
		name := d.Name()
		if name == "." || name == ".." {
			return nil
		}
		if strings.HasPrefix(name, ".") && !hidden && path != root {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && skip[name] {
			return fs.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if q != "" {
			base := strings.ToLower(rel)
			if scoreName(q, base) == 0 && scoreName(q, strings.ToLower(name)) == 0 {
				return nil
			}
		}
		detail := "file"
		if d.IsDir() {
			detail = "dir"
		}
		hits = append(hits, listItem{Key: rel, Label: rel, Detail: detail})
		return nil
	})
	// Rank by fuzzy score.
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			si := scoreName(q, strings.ToLower(hits[i].Label))
			sj := scoreName(q, strings.ToLower(hits[j].Label))
			if sj > si {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if len(hits) > 24 {
		hits = hits[:24]
	}
	return hits
}

func (m *model) refreshFileSearch() {
	tok, ok := parseAtToken(m.ta.Value())
	if !ok {
		if m.overlay == overlayFiles {
			m.overlay = overlayNone
		}
		return
	}
	m.overlay = overlayFiles
	m.filter = tok.Query
	m.items = listWorkspaceFiles(m.opts.CWD, tok.Query, tok.Hidden)
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) acceptFileSearch(tab bool) {
	if m.cursor >= len(m.items) {
		m.overlay = overlayNone
		return
	}
	it := m.items[m.cursor]
	tok, ok := parseAtToken(m.ta.Value())
	if !ok {
		m.overlay = overlayNone
		return
	}
	isDir := it.Detail == "dir"
	m.ta.SetValue(acceptAtPath(m.ta.Value(), tok, it.Key, isDir, tab))
	m.rememberChip(it.Key, isDir)
	if isDir && !tab {
		m.refreshFileSearch()
		return
	}
	if isDir && tab && strings.HasSuffix(strings.TrimSpace(m.ta.Value()), "/") {
		m.refreshFileSearch()
		return
	}
	m.overlay = overlayNone
}

func (m *model) handleFileSearchKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		return nil, true
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return nil, true
	case "down":
		if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		return nil, true
	case "tab", "enter":
		m.acceptFileSearch(true)
		return nil, true
	case "right":
		m.acceptFileSearch(false)
		return nil, true
	case ":", "ctrl+l":
		if m.cursor < len(m.items) && m.items[m.cursor].Detail != "dir" {
			rel := m.items[m.cursor].Key
			tok, ok := parseAtToken(m.ta.Value())
			if ok {
				m.ta.SetValue(strings.TrimRight(acceptAtPath(m.ta.Value(), tok, rel, false, false), " "))
			}
			cmd := m.openLineViewerFile(rel, true)
			if m.overlay == overlayLineView {
				return cmd, true
			}
		}
	}
	return nil, false
}
