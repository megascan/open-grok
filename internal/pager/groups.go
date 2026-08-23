package pager

import (
	"fmt"
	"strings"
)

type verbRun struct {
	Verb, Noun string
	Start, End int // inclusive Start, exclusive End in m.blocks
	Failed     int
	Running    bool
}

func toolName(title string) string {
	s := strings.TrimSpace(title)
	s = strings.TrimLeft(s, "▸ ")
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

func toolVerb(title string) (verb, noun string) {
	name := toolName(title)
	switch {
	case strings.Contains(name, "read"):
		return "Read", "file"
	case strings.Contains(name, "write") || strings.Contains(name, "search_replace") || strings.Contains(name, "apply_patch"):
		return "Edited", "file"
	case strings.Contains(name, "grep") || strings.Contains(name, "search") || strings.Contains(name, "glob"):
		return "Searched", "pattern"
	case strings.Contains(name, "list") || name == "ls":
		return "Listed", "dir"
	case name == "bash" || strings.Contains(name, "shell") || name == "exec":
		return "Ran", "command"
	case strings.Contains(name, "web"):
		return "Fetched", "url"
	default:
		return "Used", "tool"
	}
}

func (m *model) verbRuns() []verbRun {
	var runs []verbRun
	i := 0
	for i < len(m.blocks) {
		b := m.blocks[i]
		if b.kind != blockTool {
			i++
			continue
		}
		verb, noun := toolVerb(b.title)
		run := verbRun{Verb: verb, Noun: noun, Start: i, End: i + 1}
		if strings.TrimSpace(b.body) == "" {
			run.Running = true
		}
		j := i + 1
		for j < len(m.blocks) {
			nb := m.blocks[j]
			if nb.kind == blockToolResult {
				if nb.err {
					run.Failed++
				}
				run.End = j + 1
				j++
				continue
			}
			if nb.kind != blockTool {
				break
			}
			v2, _ := toolVerb(nb.title)
			if v2 != verb {
				break
			}
			if strings.TrimSpace(nb.body) == "" {
				run.Running = true
			}
			run.End = j + 1
			j++
		}
		runs = append(runs, run)
		i = run.End
		if i == run.Start {
			i++
		}
	}
	return runs
}

func (m *model) groupKey(run verbRun) string {
	return fmt.Sprintf("%d:%s", run.Start, run.Verb)
}

func (m *model) groupExpanded(run verbRun) bool {
	if m.expandedGroups == nil {
		return false
	}
	return m.expandedGroups[m.groupKey(run)]
}

func (m *model) toggleGroup(run verbRun) {
	if m.expandedGroups == nil {
		m.expandedGroups = map[string]bool{}
	}
	k := m.groupKey(run)
	m.expandedGroups[k] = !m.expandedGroups[k]
}

func (m *model) runForBlock(idx int) (verbRun, bool) {
	for _, r := range m.verbRuns() {
		if idx >= r.Start && idx < r.End {
			return r, true
		}
	}
	return verbRun{}, false
}

func nounPlural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	switch noun {
	case "dir":
		return "dirs"
	default:
		return noun + "s"
	}
}

func (m *model) renderVerbHeader(run verbRun, w int) string {
	n := 0
	for i := run.Start; i < run.End; i++ {
		if m.blocks[i].kind == blockTool {
			n++
		}
	}
	if n == 0 {
		n = 1
	}
	verb := run.Verb
	if run.Running {
		switch run.Verb {
		case "Read":
			verb = "Reading"
		case "Edited":
			verb = "Editing"
		case "Searched":
			verb = "Searching"
		case "Listed":
			verb = "Listing"
		case "Ran":
			verb = "Running"
		case "Fetched":
			verb = "Fetching"
		case "Used":
			verb = "Using"
		}
	}
	mark := "▸ "
	if m.groupExpanded(run) {
		mark = "▾ "
	}
	label := fmt.Sprintf("%s%s %d %s", mark, verb, n, nounPlural(run.Noun, n))
	if run.Failed > 0 {
		label += fmt.Sprintf(" · %d failed", run.Failed)
	}
	inner := toolStyle.Render(label)
	return toolCard.Width(w - 2).Render(inner)
}
