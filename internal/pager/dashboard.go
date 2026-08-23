package pager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sessionbus"
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
	m.dashRename = ""
	m.peekReply = ""
	m.peekFocus = true
	m.peekQSel = -1
	m.dashDetails = false
	m.dashListFocus = false
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
	local := map[string]bool{}
	for _, r := range raw {
		local[r.Sess.ID] = true
	}
	for _, live := range sessionbus.LiveSessions() {
		if local[live.SessionID] {
			continue
		}
		if m.sess != nil && m.sess.ID == live.SessionID {
			continue
		}
		title := ""
		if live.Title != nil {
			title = *live.Title
		}
		raw = append(raw, dashRow{
			Sess:  agent.Session{ID: live.SessionID, Title: title, CWD: live.CWD, ModelID: live.ModelID},
			State: "inactive",
		})
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
		out = append(out, dashRow{State: "new", Header: "[+ New Agent]"})
		return out
	}
	order := []string{"needs-input", "working", "idle", "inactive"}
	labels := map[string]string{"needs-input": "Needs input", "working": "Working", "idle": "Idle", "inactive": "Inactive"}
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
		hdr := labels[st]
		if st == "inactive" && m.inactiveCollapsed && m.dashFilter == "" {
			hdr = fmt.Sprintf("Inactive %d", len(rows))
		}
		out = append(out, dashRow{Header: hdr, State: "header"})
		if st == "inactive" && m.inactiveCollapsed && m.dashFilter == "" {
			continue
		}
		if st == "idle" && !m.dashIdleExpand && m.dashFilter == "" && len(rows) > 8 {
			out = append(out, rows[:8]...)
			out = append(out, dashRow{State: "more", More: len(rows) - 8, Header: fmt.Sprintf("%d more idle", len(rows)-8)})
		} else {
			out = append(out, rows...)
		}
	}
	out = append(out, dashRow{State: "new", Header: "[+ New Agent]"})
	return out
}

func (m *model) fillDashPeek() {
	m.items = nil
	if m.cursor >= len(m.dashRows) {
		return
	}
	row := m.dashRows[m.cursor]
	if row.Sess.ID == "" || row.State == "inactive" || row.State == "new" {
		return
	}
	s := row.Sess
	n := 0
	for i := len(s.Messages) - 1; i >= 0 && n < 6; i-- {
		msg := s.Messages[i]
		if msg.Role != types.RoleUser && msg.Role != types.RoleAssistant {
			continue
		}
		if msg.Role == types.RoleUser {
			m.items = append([]listItem{{Label: strings.TrimSpace(promptArrow), Detail: truncate(msg.Content, 120)}}, m.items...)
		} else {
			m.items = append([]listItem{{Detail: truncate(msg.Content, 120)}}, m.items...)
		}
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
		if r.Header != "" && (r.State == "header" || r.State == "more" || r.State == "new") {
			mark := "▾ "
			if r.State == "more" || (r.State == "header" && m.inactiveCollapsed && strings.HasPrefix(r.Header, "Inactive")) {
				mark = "▸ "
			}
			if r.State == "new" {
				mark = ""
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
		case "inactive":
			dot = "○"
			st = dimStyle.Render("inactive")
		}
		pin := " "
		if r.Pinned {
			pin = "★"
		}
		title := r.Sess.Title
		if title == "" {
			title = "(untitled)"
		}
		stamp := ""
		if !r.Sess.UpdatedAt.IsZero() {
			stamp = r.Sess.UpdatedAt.Format("15:04")
		}
		line := fmt.Sprintf("%s%s %s · %s   %s   %s", pin, dot, title, shortID(r.Sess.ID), st, stamp)
		if i == m.cursor {
			line = selStyle.Render(line)
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n")
	b.WriteString(m.renderDashPeekPanel())
	return b.String()
}

func (m *model) peekRow() (dashRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.dashRows) {
		return dashRow{}, false
	}
	r := m.dashRows[m.cursor]
	if r.Sess.ID == "" || r.State == "inactive" || r.State == "new" || r.State == "header" || r.State == "more" {
		return dashRow{}, false
	}
	return r, true
}

func (m *model) peekLiveCurrent(row dashRow) bool {
	return m.sess != nil && m.sess.ID == row.Sess.ID
}

func (m *model) peekQuestionLive() bool {
	row, ok := m.peekRow()
	return ok && m.question != nil && m.peekLiveCurrent(row)
}

func (m *model) peekPermissionLive() bool {
	row, ok := m.peekRow()
	return ok && m.perm != nil && m.peekLiveCurrent(row)
}

func (m *model) peekInputLive() bool {
	return m.peekQuestionLive() || m.peekPermissionLive()
}

func (m *model) peekOptionLabels() []string {
	if m.peekPermissionLive() {
		var labels []string
		for _, c := range m.permChoices() {
			labels = append(labels, c.Label)
		}
		if len(labels) > 9 {
			labels = labels[:9]
		}
		return labels
	}
	if !m.peekQuestionLive() {
		return nil
	}
	opts := m.question.current().Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	if !m.questionLocal() {
		opts = append(append([]string{}, opts...), "Other")
	}
	if len(opts) > 9 {
		opts = opts[:9]
	}
	return opts
}

func (m *model) peekRejectIndex() int {
	if m.peekPermissionLive() {
		for i, c := range m.permChoices() {
			if i >= 9 {
				break
			}
			if c.ID == "followup" {
				return i
			}
		}
		return -1
	}
	if m.peekQuestionLive() && !m.questionLocal() {
		n := len(m.peekOptionLabels())
		if n == 0 {
			return -1
		}
		return n - 1
	}
	return -1
}

func (m *model) peekQuestionTitle() string {
	if m.peekPermissionLive() {
		return m.permTitle()
	}
	if !m.peekQuestionLive() {
		return ""
	}
	cur := m.question.current()
	n := len(m.question.items())
	if n > 1 {
		return fmt.Sprintf("(%d/%d) %s", m.question.Index+1, n, cur.Question)
	}
	return cur.Question
}

func (m *model) renderDashPeekPanel() string {
	var b strings.Builder
	if m.renaming {
		return titleStyle.Render(" Rename: ") + m.dashRename + "█\n"
	}
	_, peek := m.peekRow()
	if peek {
		b.WriteString(dimStyle.Render("╭ peek") + "\n")
		if m.peekInputLive() {
			b.WriteString(claimStyle.Render("▸ "+m.peekQuestionTitle()) + "\n")
			opts := m.peekOptionLabels()
			reject := m.peekRejectIndex()
			for i, o := range opts {
				label := o
				if i == reject {
					if strings.TrimSpace(m.peekReply) != "" && m.peekQSel == i {
						label = m.peekReply
					} else if m.peekQuestionLive() {
						label = peekOtherPlaceholder
					} else {
						label = permFollowupLabel
					}
				}
				line := fmt.Sprintf("  %d. %s", i+1, label)
				if m.peekQSel == i {
					line = selStyle.Render("▸ " + strconv.Itoa(i+1) + ". " + label)
				}
				b.WriteString(line + "\n")
			}
			b.WriteString(dimStyle.Render("1-9 select  ·  enter answer  ·  esc back") + "\n")
			if banner := m.renderVoiceBanner(); banner != "" {
				b.WriteString(banner + "\n")
			}
			return b.String()
		}
		for _, it := range m.items {
			if it.Label == "" {
				b.WriteString("  " + dimStyle.Render(it.Detail) + "\n")
				continue
			}
			b.WriteString("  " + it.Label + " " + dimStyle.Render(it.Detail) + "\n")
		}
		b.WriteString("❯ reply " + m.peekReply + "█\n")
		b.WriteString(dimStyle.Render("enter send  ·  ctrl+s send+open  ·  empty enter opens  ·  esc clears reply") + "\n")
		if banner := m.renderVoiceBanner(); banner != "" {
			b.WriteString(banner + "\n")
		}
		return b.String()
	}
	if banner := m.renderVoiceBanner(); banner != "" {
		b.WriteString(banner + "\n")
	}
	b.WriteString("❯ Dispatch a new agent  " + m.dashDraft + "█\n")
	b.WriteString(dimStyle.Render("enter spawn  ·  ctrl+s spawn+open  ·  ctrl+r rename  ·  ctrl+t pin  ·  ctrl+g group  ·  ctrl+x stop/delete  ·  ctrl+/ search  ·  ctrl+u clear  ·  esc never clears draft") + "\n")
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
	s := msg.String()
	_, peek := m.peekRow()
	questionPeek := m.peekInputLive()

	if s == "esc" {
		if peek {
			if m.peekReply != "" {
				m.peekReply = ""
				return nil
			}
			m.focusNewAgent()
			return nil
		}
		if !m.dashListFocus {
			m.dashListFocus = true
			return nil
		}
		m.leaveDashboard()
		return nil
	}
	if s == "ctrl+\\" {
		m.leaveDashboard()
		return nil
	}

	if questionPeek {
		if idx := optionIndexFromKey(s, peekOptionCount(m)); idx >= 0 {
			if m.peekQSel == idx {
				m.peekQSel = -1
			} else {
				m.peekQSel = idx
			}
			return nil
		}
		switch s {
		case "up":
			if m.peekQSel > 0 {
				m.peekQSel--
				return nil
			}
			m.moveDashCursor(-1)
			return nil
		case "down":
			n := peekOptionCount(m)
			if m.peekQSel >= 0 && m.peekQSel+1 < n {
				m.peekQSel++
				return nil
			}
			m.moveDashCursor(1)
			return nil
		case "enter", "right":
			if m.peekQSel >= 0 {
				return m.answerPeekQuestion()
			}
			return m.attachDashRow()
		}
	}

	nav := m.dashListFocus || s == "up" || s == "down"
	if peek && m.peekFocus && (s == "j" || s == "k") {
		nav = false
	}
	if nav {
		switch s {
		case "up", "k":
			m.moveDashCursor(-1)
			return nil
		case "down", "j":
			m.moveDashCursor(1)
			return nil
		}
	}

	switch s {
	case "enter":
		return m.dashEnter(false)
	case "ctrl+s":
		return m.dashEnter(true)
	case "right", "l":
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "header" && m.inactiveCollapsed && strings.HasPrefix(m.dashRows[m.cursor].Header, "Inactive") {
			m.inactiveCollapsed = false
			m.refreshDashboard()
			return nil
		}
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "more" {
			m.dashIdleExpand = true
			m.refreshDashboard()
			return nil
		}
		if peek && strings.TrimSpace(m.peekReply) == "" {
			return m.attachDashRow()
		}
	case "left", "h":
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "header" && strings.HasPrefix(m.dashRows[m.cursor].Header, "Inactive") && !m.inactiveCollapsed {
			m.inactiveCollapsed = true
			m.refreshDashboard()
			return nil
		}
	case "ctrl+n":
		return m.newDashSession()
	case "ctrl+r":
		if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].Sess.ID != "" {
			m.renaming = true
			m.dashRename = m.dashRows[m.cursor].Sess.Title
		}
		return nil
	case "ctrl+t":
		m.togglePin()
		return nil
	case "ctrl+x":
		return m.dashStopOrDelete()
	case "ctrl+/":
		m.dashSearch = !m.dashSearch
		m.dashFilter = ""
		m.refreshDashboard()
		return nil
	case "ctrl+g":
		if m.dashGroup == "dir" {
			m.dashGroup = "state"
		} else {
			m.dashGroup = "dir"
		}
		m.refreshDashboard()
		return nil
	case "ctrl+u":
		if peek {
			m.peekReply = ""
		} else {
			m.dashDraft = ""
		}
		return nil
	case "?":
		m.status = "Enter open · Ctrl+S send+open · Ctrl+R rename · Ctrl+T pin · Ctrl+G group · Ctrl+X twice delete · Esc never clears draft (Ctrl+U)"
		return nil
	case "tab":
		if peek {
			m.peekFocus = !m.peekFocus
			m.dashListFocus = !m.peekFocus
		} else {
			m.dashListFocus = !m.dashListFocus
		}
		return nil
	case "backspace":
		if peek && m.peekFocus {
			if len(m.peekReply) > 0 {
				m.peekReply = m.peekReply[:len(m.peekReply)-1]
			}
			return nil
		}
		if !m.dashListFocus && len(m.dashDraft) > 0 {
			m.dashDraft = m.dashDraft[:len(m.dashDraft)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		if peek && (m.peekFocus || !m.dashListFocus) && (!questionPeek || m.peekQSel == m.peekRejectIndex()) {
			m.peekFocus = true
			m.dashListFocus = false
			m.peekReply += string(msg.Runes)
			return nil
		}
		if !peek {
			m.dashListFocus = false
			m.dashDraft += string(msg.Runes)
		}
	}
	return nil
}

func peekOptionCount(m *model) int {
	return len(m.peekOptionLabels())
}

func (m *model) moveDashCursor(delta int) {
	if len(m.dashRows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.dashRows) {
		m.cursor = len(m.dashRows) - 1
	}
	m.peekReply = ""
	m.peekQSel = -1
	m.peekFocus = true
	m.fillDashPeek()
}

func (m *model) focusNewAgent() {
	m.peekReply = ""
	m.peekQSel = -1
	m.peekFocus = false
	for i, r := range m.dashRows {
		if r.State == "new" {
			m.cursor = i
			m.fillDashPeek()
			return
		}
	}
}

func (m *model) dashEnter(openChat bool) tea.Cmd {
	if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "more" {
		m.dashIdleExpand = true
		m.refreshDashboard()
		return nil
	}
	if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "header" {
		if strings.HasPrefix(m.dashRows[m.cursor].Header, "Inactive") {
			m.inactiveCollapsed = !m.inactiveCollapsed
			m.refreshDashboard()
		}
		return nil
	}
	if m.cursor < len(m.dashRows) && m.dashRows[m.cursor].State == "new" && strings.TrimSpace(m.dashDraft) == "" && strings.TrimSpace(m.peekReply) == "" {
		return m.newDashSession()
	}
	if _, peek := m.peekRow(); peek {
		text := strings.TrimSpace(m.peekReply)
		if text == "" {
			return m.attachDashRow()
		}
		if openChat {
			m.peekReply = ""
			m.peekQSel = -1
			if cmd := m.attachDashRow(); cmd != nil {
				return tea.Batch(cmd, m.submit(text))
			}
			return m.submit(text)
		}
		return m.sendPeekReply()
	}
	if strings.TrimSpace(m.dashDraft) != "" {
		return m.dispatchAgent(m.dashDraft, openChat)
	}
	return m.attachDashRow()
}

func (m *model) answerPeekQuestion() tea.Cmd {
	if m.peekQSel < 0 {
		return nil
	}
	if m.peekPermissionLive() {
		choices := m.permChoices()
		if m.peekQSel >= len(choices) {
			return nil
		}
		c := choices[m.peekQSel]
		if c.ID == "followup" {
			m.permFollowup = strings.TrimSpace(m.peekReply)
			m.permFollowupMode = true
		}
		m.peekQSel = -1
		m.peekReply = ""
		cmd := m.applyPermChoice(c)
		m.refreshDashboard()
		return cmd
	}
	if !m.peekQuestionLive() {
		return nil
	}
	opts := m.question.current().Options
	if len(opts) == 0 {
		opts = []string{"yes", "no"}
	}
	sel := m.peekQSel
	m.peekQSel = -1
	if sel == len(opts) {
		ans := strings.TrimSpace(m.peekReply)
		m.peekReply = ""
		cmd := m.replyQuestion(ans)
		m.refreshDashboard()
		return cmd
	}
	if sel >= len(opts) {
		return nil
	}
	m.selectQuestionOption(sel)
	cmd := m.advanceOrSubmitQuestion()
	m.peekReply = ""
	m.refreshDashboard()
	return cmd
}

func (m *model) sendPeekReply() tea.Cmd {
	text := strings.TrimSpace(m.peekReply)
	row, ok := m.peekRow()
	if !ok || text == "" {
		return nil
	}
	sess, err := agent.LoadSession(m.opts.CWD, row.Sess.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.peekReply = ""
	m.peekQSel = -1
	if m.sess != nil && m.sess.ID == sess.ID && m.busy {
		m.queue = append(m.queue, text)
		m.status = "queued follow-up"
		return nil
	}
	arm := m.bindSession(sess, false)
	return tea.Batch(arm, m.submit(text))
}

func (m *model) bindSession(sess *agent.Session, openChat bool) tea.Cmd {
	prev := m.screen
	cmd := m.attachSession(sess)
	if openChat {
		m.layoutChat()
		return cmd
	}
	m.screen = prev
	return cmd
}

func (m *model) newDashSession() tea.Cmd {
	sess, err := agent.NewSession(m.dashCWD, m.opts.Model, m.permMode == permAlways)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.dashDetails = true
	cmd := m.attachSession(sess)
	m.layoutChat()
	return cmd
}

func (m *model) attachableDashIDs() []string {
	ids := make([]string, 0, len(m.dashRows))
	seen := map[string]bool{}
	for _, r := range m.dashRows {
		if r.Sess.ID == "" || r.State == "inactive" || r.State == "new" || r.State == "header" || r.State == "more" {
			continue
		}
		if seen[r.Sess.ID] {
			continue
		}
		seen[r.Sess.ID] = true
		ids = append(ids, r.Sess.ID)
	}
	return ids
}

func (m *model) cycleDashDetails(delta int) tea.Cmd {
	ids := m.attachableDashIDs()
	if len(ids) == 0 || m.sess == nil {
		return nil
	}
	idx := 0
	for i, id := range ids {
		if id == m.sess.ID {
			idx = i
			break
		}
	}
	n := len(ids)
	idx = (idx + delta%n + n) % n
	sess, err := agent.LoadSession(m.opts.CWD, ids[idx])
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.dashDetails = true
	cmd := m.attachSession(sess)
	m.layoutChat()
	return cmd
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
	m.dashDetails = true
	cmd := m.attachSession(sess)
	m.layoutChat()
	return cmd
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
	arm := m.bindSession(sess, attach)
	if attach {
		m.dashDetails = true
	}
	return tea.Batch(arm, m.submit(prompt))
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
