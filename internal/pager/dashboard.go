package pager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

type dashRow struct {
	Sess   agent.Session
	State  string // needs-input, working, idle, header, more
	Pinned bool
	Header string
	More   int
}

func pinPath() string {
	return filepath.Join(config.Home(), "dashboard-pins.json")
}

func loadPins() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(pinPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func savePins(p map[string]bool) {
	data, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(pinPath(), data, 0o600)
}

func (m *model) openDashboard() {
	if !m.cfg.DashboardEnabled() {
		m.appendSys("dashboard disabled (ENTROPY_AGENT_DASHBOARD=0 or [dashboard].enabled=false)")
		return
	}
	if m.screenMd == modeMinimal {
		m.appendSys("/dashboard isn't available in minimal mode. Run /fullscreen to switch this session.")
		return
	}
	m.fromDash = m.screen
	m.screen = screenDashboard
	m.overlay = overlayNone
	m.dashSearch = false
	m.dashFilter = ""
	m.dashDraft = ""
	m.dashRename = ""
	if m.dashGroup == "" {
		m.dashGroup = "state"
	}
	m.refreshDashboard()
}

func (m *model) refreshDashboard() {
	pins := loadPins()
	list, _ := agent.ListSessions(m.opts.CWD)
	raw := make([]dashRow, 0, len(list))
	for _, s := range list {
		st := "idle"
		if m.sess != nil && m.sess.ID == s.ID && m.busy {
			st = "working"
		}
		if (m.perm != nil || m.question != nil || m.plan != nil) && m.sess != nil && m.sess.ID == s.ID {
			st = "needs-input"
		}
		raw = append(raw, dashRow{Sess: s, State: st, Pinned: pins[s.ID]})
	}
	if m.dashFilter != "" {
		f := strings.ToLower(m.dashFilter)
		var keep []dashRow
		for _, r := range raw {
			blob := strings.ToLower(r.Sess.Title + " " + r.Sess.ID + " " + r.Sess.ModelID + " " + r.Sess.CWD)
			if strings.Contains(blob, f) {
				keep = append(keep, r)
			}
		}
		raw = keep
	}
	m.dashRows = m.groupDashRows(raw)
	if m.cursor >= len(m.dashRows) {
		m.cursor = 0
	}
	m.fillDashPeek()
}

func (m *model) groupDashRows(raw []dashRow) []dashRow {
	if m.dashGroup == "dir" {
		buckets := map[string][]dashRow{}
		var order []string
		for _, r := range raw {
			k := r.Sess.CWD
			if k == "" {
				k = m.opts.CWD
			}
			if _, ok := buckets[k]; !ok {
				order = append(order, k)
			}
			buckets[k] = append(buckets[k], r)
		}
		var out []dashRow
		for _, k := range order {
			out = append(out, dashRow{Header: k, State: "header"})
			out = append(out, buckets[k]...)
		}
		return out
	}
	order := []string{"needs-input", "working", "idle"}
	labels := map[string]string{"needs-input": "Needs input", "working": "Working", "idle": "Idle"}
	buckets := map[string][]dashRow{}
	for _, r := range raw {
		if r.Pinned {
			buckets["pinned"] = append(buckets["pinned"], r)
			continue
		}
		buckets[r.State] = append(buckets[r.State], r)
	}
	var out []dashRow
	if len(buckets["pinned"]) > 0 {
		out = append(out, dashRow{Header: "Pinned", State: "header"})
		out = append(out, buckets["pinned"]...)
	}
	for _, st := range order {
		rows := buckets[st]
		if len(rows) == 0 {
			continue
		}
		out = append(out, dashRow{Header: labels[st], State: "header"})
		if st == "idle" && !m.dashIdleExpand && m.dashFilter == "" && len(rows) > 8 {
			out = append(out, rows[:8]...)
			out = append(out, dashRow{State: "more", More: len(rows) - 8, Header: fmt.Sprintf("%d more idle", len(rows)-8)})
		} else {
			out = append(out, rows...)
		}
	}
	return out
}

func (m *model) fillDashPeek() {
	m.items = nil
	if m.cursor >= len(m.dashRows) {
		return
	}
	row := m.dashRows[m.cursor]
	if row.Sess.ID == "" {
		return
	}
	s := row.Sess
	n := 0
	for i := len(s.Messages) - 1; i >= 0 && n < 6; i-- {
		msg := s.Messages[i]
		if msg.Role != types.RoleUser && msg.Role != types.RoleAssistant {
			continue
		}
		label := "YOU"
		if msg.Role == types.RoleAssistant {
			label = "ENTROPY"
		}
		m.items = append([]listItem{{Label: label, Detail: truncate(msg.Content, 120)}}, m.items...)
		n++
	}
	if len(m.items) == 0 {
		m.items = []listItem{{Label: "(empty session)"}}
	}
}

func (m *model) viewDashboard() string {
	var b strings.Builder
	agents := 0
	for _, r := range m.dashRows {
		if r.Sess.ID != "" {
			agents++
		}
	}
	b.WriteString(headerBar.Width(max(20, m.width)).Render(
		fmt.Sprintf(" Entropy · Dashboard — %d agents  %s", agents, version.String()),
	) + "\n")
	if m.dashSearch {
		b.WriteString(claimStyle.Render(" Search: "+m.dashFilter+"█") + "\n")
	}
	if len(m.dashRows) == 0 {
		b.WriteString(dimStyle.Render("  (no sessions — type below to dispatch)") + "\n")
	}
	for i, r := range m.dashRows {
		if r.Header != "" && (r.State == "header" || r.State == "more") {
			mark := "▾ "
			if r.State == "more" {
				mark = "▸ "
			}
			line := dimStyle.Render(mark + r.Header)
			if i == m.cursor {
				line = selStyle.Render(mark + r.Header)
			}
			b.WriteString(line + "\n")
			continue
		}
		dot := "○"
		st := dimStyle.Render("idle")
		switch r.State {
		case "working":
			dot = "⋅"
			st = okStyle.Render("working")
		case "needs-input":
			dot = "●"
			st = claimStyle.Render("awaiting")
		}
		pin := " "
		if r.Pinned {
			pin = "★"
		}
		title := r.Sess.Title
		if title == "" {
			title = "(untitled)"
		}
		line := fmt.Sprintf("%s%s %s · %s   %s   %s", pin, dot, title, shortID(r.Sess.ID), st, r.Sess.UpdatedAt.Format("15:04"))
		if i == m.cursor {
			line = selStyle.Render(line)
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n")
	b.WriteString(dimStyle.Render(" peek") + "\n")
	for _, it := range m.items {
		b.WriteString("  " + it.Label + "  " + dimStyle.Render(it.Detail) + "\n")
	}
	if m.renaming {
		b.WriteString("\n" + titleStyle.Render(" Rename: ") + m.dashRename + "█\n")
	} else {
		b.WriteString("\n❯ " + m.dashDraft + "█\n")
		b.WriteString(dimStyle.Render("enter open  ·  ctrl+s send+open  ·  ctrl+r rename  ·  ctrl+t pin  ·  ctrl+g group  ·  ctrl+x stop/delete  ·  ctrl+/ search  ·  ctrl+u clear  ·  ? help  ·  esc back"))
	}
	return b.String()
}

func (m *model) handleDashboardKey(msg tea.KeyMsg) tea.Cmd {
	if m.renaming {
		return m.handleDashRename(msg)
	}
	if m.dashSearch {
		switch msg.Type {
		case tea.KeyEsc:
			m.dashSearch = false
			m.dashFilter = ""
			m.refreshDashboard()
			return nil
		case tea.KeyBackspace:
			if len(m.dashFilter) > 0 {
				m.dashFilter = m.dashFilter[:len(m.dashFilter)-1]
				m.refreshDashboard()
			}
			return nil
		case tea.KeyRunes:
			m.dashFilter += string(msg.Runes)
			m.refreshDashboard()
			return nil
		}
	}
	switch msg.String() {
	case "ctrl+\\", "esc":
		if m.dashDraft != "" && msg.String() == "esc" {
			return nil
		}
		m.leaveDashboard()
		return nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.fillDashPeek()
		}
	case "down", "j":
		if m.cursor+1 < len(m.dashRows) {
			m.cursor++
			m.fillDashPeek()
		}
	case "enter":
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "more" {
			m.dashIdleExpand = true
			m.refreshDashboard()
			return nil
		}
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "header" {
			return nil
		}
		if strings.TrimSpace(m.dashDraft) != "" {
			return m.dispatchAgent(m.dashDraft, false)
		}
		return m.attachDashRow()
	case "ctrl+s":
		if strings.TrimSpace(m.dashDraft) != "" {
			return m.dispatchAgent(m.dashDraft, true)
		}
		return m.attachDashRow()
	case "ctrl+r":
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].Sess.ID != "" {
			m.renaming = true
			m.dashRename = m.dashRows[m.cursor].Sess.Title
		}
	case "ctrl+t":
		m.togglePin()
	case "ctrl+x":
		return m.dashStopOrDelete()
	case "ctrl+/":
		m.dashSearch = !m.dashSearch
		m.dashFilter = ""
		m.refreshDashboard()
	case "ctrl+g":
		if m.dashGroup == "dir" {
			m.dashGroup = "state"
		} else {
			m.dashGroup = "dir"
		}
		m.refreshDashboard()
	case "ctrl+u", "ctrl+c":
		m.dashDraft = ""
	case "?":
		m.status = "Enter open · Ctrl+S send+open · Ctrl+R rename · Ctrl+T pin · Ctrl+G group · Ctrl+X twice delete · Esc never clears draft (Ctrl+U)"
	case "backspace":
		if len(m.dashDraft) > 0 {
			m.dashDraft = m.dashDraft[:len(m.dashDraft)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.dashDraft += string(msg.Runes)
		}
	}
	return nil
}

func (m *model) handleDashRename(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.renaming = false
		m.dashRename = ""
	case tea.KeyEnter:
		if m.cursor < len(m.dashRows) {
			id := m.dashRows[m.cursor].Sess.ID
			sess, err := agent.LoadSession(m.opts.CWD, id)
			if err == nil {
				sess.Title = m.dashRename
				_ = sess.Persist()
			}
			if m.sess != nil && m.sess.ID == id {
				m.sess.Title = m.dashRename
				_ = m.sess.Persist()
			}
		}
		m.renaming = false
		m.dashRename = ""
		m.refreshDashboard()
	case tea.KeyBackspace:
		if len(m.dashRename) > 0 {
			m.dashRename = m.dashRename[:len(m.dashRename)-1]
		}
	case tea.KeyRunes:
		m.dashRename += string(msg.Runes)
	}
	return nil
}

func (m *model) togglePin() {
	if m.cursor >= len(m.dashRows) || m.dashRows[m.cursor].Sess.ID == "" {
		return
	}
	pins := loadPins()
	id := m.dashRows[m.cursor].Sess.ID
	if pins[id] {
		delete(pins, id)
	} else {
		pins[id] = true
	}
	savePins(pins)
	m.refreshDashboard()
}

func (m *model) dashStopOrDelete() tea.Cmd {
	if m.cursor >= len(m.dashRows) || m.dashRows[m.cursor].Sess.ID == "" {
		return nil
	}
	row := m.dashRows[m.cursor]
	if m.sess != nil && m.sess.ID == row.Sess.ID && m.busy {
		return m.requestCancelTurn()
	}
	now := time.Now()
	if m.dashArmed.IsZero() || now.Sub(m.dashArmed) > 2*time.Second {
		m.dashArmed = now
		m.status = "ctrl+x again to delete " + shortID(row.Sess.ID)
		return nil
	}
	sess, err := agent.LoadSession(m.opts.CWD, row.Sess.ID)
	if err == nil {
		_ = agent.DeleteSession(sess)
	}
	if m.sess != nil && m.sess.ID == row.Sess.ID {
		m.sess = nil
	}
	m.dashArmed = time.Time{}
	m.refreshDashboard()
	return nil
}

func (m *model) attachDashRow() tea.Cmd {
	if m.cursor >= len(m.dashRows) || m.dashRows[m.cursor].Sess.ID == "" {
		return nil
	}
	sess, err := agent.LoadSession(m.opts.CWD, m.dashRows[m.cursor].Sess.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.attachSession(sess)
	m.layoutChat()
	return nil
}

func (m *model) dispatchAgent(prompt string, attach bool) tea.Cmd {
	sess, err := agent.NewSession(m.dashCWD, m.opts.Model, m.permMode == permAlways)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	sess.Title = truncate(prompt, 40)
	_ = sess.Persist()
	m.dashDraft = ""
	if attach {
		m.attachSession(sess)
		m.layoutChat()
		return m.submit(prompt)
	}
	m.refreshDashboard()
	return nil
}

func (m *model) leaveDashboard() {
	if m.fromDash == screenChat || m.sess != nil {
		m.screen = screenChat
		if m.sess != nil {
			m.layoutChat()
		} else {
			m.screen = screenWelcome
			m.refreshWelcome()
		}
		return
	}
	m.screen = screenWelcome
	m.refreshWelcome()
}
