package pager

import (
	"fmt"
	"strings"
)

type verbRun struct {
	Kind       verbKind
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
	k := classifyTool(title)
	return k.verb(false), k.noun(1)
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
		kind := classifyTool(b.title)
		if !kind.groupable() {
			i++
			continue
		}
		run := verbRun{Kind: kind, Verb: kind.verb(false), Noun: kind.noun(1), Start: i, End: i + 1}
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
			k2 := classifyTool(nb.title)
			if k2 != kind || !k2.groupable() {
				break
			}
			if strings.TrimSpace(nb.body) == "" {
				run.Running = true
			}
			run.End = j + 1
			j++
		}
		n := 0
		for k := run.Start; k < run.End; k++ {
			if m.blocks[k].kind == blockTool {
				n++
			}
		}
		if n >= 2 {
			run.Verb = kind.verb(run.Running)
			run.Noun = kind.noun(n)
			runs = append(runs, run)
		}
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

func (m *model) toolCount(run verbRun) int {
	n := 0
	for i := run.Start; i < run.End; i++ {
		if m.blocks[i].kind == blockTool {
			n++
		}
	}
	return n
}

func (m *model) renderVerbHeader(run verbRun, w int) string {
	n := m.toolCount(run)
	if n == 0 {
		n = 1
	}
	verb := run.Kind.verb(run.Running)
	mark := "▸ "
	if m.groupExpanded(run) {
		mark = "▾ "
	}
	label := fmt.Sprintf("%s%s %d %s", mark, verb, n, run.Kind.noun(n))
	if run.Failed > 0 {
		label += fmt.Sprintf(" · %d failed", run.Failed)
	}
	var b strings.Builder
	writeAccent(&b, toolStyle.Render(label))
	_ = w
	return strings.TrimRight(b.String(), "\n")
}
