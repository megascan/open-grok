package pager

import (
	"sort"
	"strings"
)

func (m *model) openTasks() {
	if m.sess == nil {
		m.appendSys("No active session")
		return
	}
	m.overlay = overlayTasks
	m.filter = ""
	m.items = m.taskItems()
	m.cursor = 0
}

func (m *model) taskItems() []listItem {
	type row struct {
		running bool
		label   string
		detail  string
		key     string
		ord     string
	}
	var rows []row
	if m.sess != nil && m.sess.Team != nil {
		agents := m.sess.Team.Snapshot()
		sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
		for _, a := range agents {
			label := a.Label
			if label == "" {
				label = a.ID
			}
			detail := a.Status
			if a.Result != "" {
				detail += "  " + truncate(a.Result, 40)
			}
			rows = append(rows, row{
				running: a.Status == "running",
				label:   label,
				detail:  detail,
				key:     "agent:" + a.ID,
				ord:     a.ID,
			})
		}
	}
	for _, j := range m.loops {
		st := j.Every.String()
		running := !j.stopped
		if j.stopped {
			st = "stopped"
		} else {
			st = "running  " + st
		}
		rows = append(rows, row{
			running: running,
			label:   "Task " + j.ID,
			detail:  st + "  " + truncate(j.Prompt, 40),
			key:     "loop:" + j.ID,
			ord:     j.ID,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].running != rows[j].running {
			return rows[i].running
		}
		return rows[i].ord > rows[j].ord
	})
	if len(rows) == 0 {
		return []listItem{{Label: "(no background tasks or subagents)"}}
	}
	out := make([]listItem, len(rows))
	for i, r := range rows {
		out[i] = listItem{Key: r.key, Label: r.label, Detail: r.detail}
	}
	return out
}

// runningTaskCount is used by the cancel-turn modal and header badges.
func (m *model) runningTaskCount() int {
	n := 0
	if m.sess != nil && m.sess.Team != nil {
		n += m.sess.Team.RunningCount()
	}
	for _, j := range m.loops {
		if !j.stopped {
			n++
		}
	}
	return n
}

func joinSep(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "  "+contextBarSep+"  ")
}
