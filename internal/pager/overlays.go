package pager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/foreign"
	"github.com/megascan/open-grok/internal/hooks"
	"github.com/megascan/open-grok/internal/tools"
)

func (m *model) viewWithOverlay(base string) string {
	if m.isPromptOverlay() {
		return base
	}
	box := m.renderOverlay()
	if box == "" {
		return base
	}
	w := lipgloss.Width(box)
	h := lipgloss.Height(box)
	x := max(0, (m.width-w)/2)
	y := max(0, (m.height-h)/3)
	return placeOverlay(base, box, x, y, m.width, m.height)
}

func placeOverlay(base, box string, x, y, width, height int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	for i, bl := range boxLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		line := baseLines[row]
		plain := stripANSI(line)
		for lipgloss.Width(plain) < x {
			plain += " "
		}
		prefix := cutANSI(line, x)
		if lipgloss.Width(stripANSI(prefix)) < x {
			prefix += strings.Repeat(" ", x-lipgloss.Width(stripANSI(prefix)))
		}
		baseLines[row] = prefix + bl
		_ = width
	}
	return strings.Join(baseLines, "\n")
}

func (m *model) renderOverlay() string {
	w := min(72, max(40, m.width-8))
	switch m.overlay {
	case overlayQuestion:
		return m.renderQuestion(w)
	case overlayDoctor:
		return m.renderDoctor(w)
	case overlayPlan:
		return m.renderPlanApproval(w)
	case overlayCancelTurn:
		return m.renderCancelTurn(w)
	case overlayWorktree:
		return m.renderWorktreeDialog(w)
	case overlayImportClaude:
		return m.renderImportClaude(w)
	case overlayRewind:
		return m.renderRewind(w)
	case overlayBtw:
		return m.renderBtwComposer(w)
	case overlayBlock:
		return m.renderBlockViewer(w)
	case overlayShortcuts:
		return m.renderShortcuts(w)
	case overlayGoal:
		return m.renderGoalDetail(w)
	case overlayUsage:
		return m.renderUsage(w)
	case overlayMemory:
		return m.renderMemory(w)
	case overlayAgents:
		return m.renderAgents(w)
	case overlayLineView:
		return m.renderLineViewer(w)
	case overlayWorkflows:
		return m.renderWorkflows(w)
	case overlayVoice:
		return ""
	case overlayLogin:
		return m.renderLoginOverlay(w)
	case overlayFiles, overlayPalette, overlayHistory:
		return ""
	case overlayPermission:
		return m.renderPermission(w)
	case overlayConfirm:
		return m.renderConfirm(w)
	case overlayFeedback:
		return m.renderFeedback(w)
	case overlaySettings:
		return m.renderSettings(w)
	case overlayTheme:
		return m.renderThemePicker(w)
	case overlayCache:
		return m.renderCache(w)
	case overlayModel:
		return m.renderModelPicker(w)
	case overlayDashboard, overlayResume,
		overlayDocs, overlayTutorial, overlayExtensions,
		overlayPersonas, overlayFind,
		overlayHelp, overlayPrivacy, overlayAnnouncements, overlayReleaseNotes,
		overlayQueue, overlayTasks,
		overlayMCP, overlayTimeline:
		return m.renderListModal(m.overlayTitle(), w)
	default:
		return ""
	}
}

func (m *model) overlayTitle() string {
	switch m.overlay {
	case overlayDashboard:
		return "Agent Dashboard"
	case overlayResume:
		return "Resume session"
	case overlayHistory:
		return "Prompt history"
	case overlayModel:
		return "Models"
	case overlayDoctor:
		return "Doctor"
	case overlayDocs:
		return "How-to Guides"
	case overlayTutorial:
		return "Tutorial"
	case overlayExtensions:
		tabs := []string{"Hooks", "Plugins", "Marketplace", "Skills", "MCP"}
		t := tabs[m.extTab%len(tabs)]
		return "Extensions · " + t
	case overlayWorkflows:
		return "Workflows"
	case overlayPersonas:
		return "Personas"
	case overlayAgents:
		return "Agent definitions"
	case overlayFind:
		return "Find in scrollback"
	case overlayHelp:
		return "Commands"
	case overlayPrivacy:
		return "Privacy"
	case overlayAnnouncements:
		return "Announcements"
	case overlayReleaseNotes:
		return "Release notes"
	case overlayShortcuts:
		return shortcutsModalTitle
	case overlayGoal:
		return "Goal"
	case overlayUsage:
		return "Usage"
	case overlayQueue:
		return "Follow-up queue"
	case overlayTasks:
		return "Tasks"
	case overlayCache:
		return "Prompt cache"
	case overlayImportClaude:
		return "Import Claude settings"
	case overlayRewind:
		return "Rewind"
	case overlayBtw:
		return "/btw"
	case overlayBlock:
		return m.blockTitle
	case overlayMemory:
		return "Memory"
	case overlayMCP:
		return "MCP servers"
	case overlayTimeline:
		return "Jump to which turn?"
	case overlayWorktree:
		return worktreeDialogTitle
	}
	return "Entropy"
}

func (m *model) renderFeedback(w int) string {
	var b strings.Builder
	writeAccent(&b, titleStyle.Render(feedbackQuestionLabel))
	writeAccent(&b, "")
	body := m.feedback
	showPH := strings.TrimSpace(body) == ""
	if showPH {
		body = feedbackPlaceholder
	}
	innerW := max(24, w-10)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colMuted)).
		Width(innerW).
		Padding(0, 1)
	text := body + "█"
	if showPH {
		text = dimStyle.Render(feedbackPlaceholder) + "█"
	}
	writeAccent(&b, box.Render(text))
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("enter send  ·  esc discard  ·  ctrl+c clear"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) renderThemePicker(w int) string {
	return m.renderRadioPicker("Theme", "↑/↓ try  ·  Enter select  ·  Esc revert", w)
}

func (m *model) renderModelPicker(w int) string {
	return m.renderRadioPicker("Models", "↑↓  enter  ·  esc close  ·  type to filter", w)
}

func (m *model) renderRadioPicker(title, hint string, w int) string {
	var b strings.Builder
	writeAccent(&b, titleStyle.Render(title))
	if m.filter != "" {
		writeAccent(&b, dimStyle.Render("filter: "+m.filter))
	}
	writeAccent(&b, "")
	if len(m.items) == 0 {
		writeAccent(&b, dimStyle.Render("(empty)"))
	}
	start := 0
	limit := 16
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	for i, it := range m.items {
		if i < start || i >= start+limit {
			continue
		}
		label := it.Label
		if it.Detail != "" {
			label += "  " + it.Detail
		}
		writeAccent(&b, radioRow(optionHotkey(i), label, i == m.cursor))
	}
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render(hint))
	return modalBox.Width(w).Render(b.String())
}

func modelPickerDetail(mod catalog.Model) string {
	var parts []string
	if slug := strings.TrimSpace(mod.Model); slug != "" {
		parts = append(parts, slug)
	} else if mod.ID != "" {
		parts = append(parts, mod.ID)
	}
	if mod.ContextWindow > 0 {
		parts = append(parts, formatContextWindow(mod.ContextWindow))
	}
	if d := strings.TrimSpace(mod.Description); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " · ")
}

func formatContextWindow(tokens int) string {
	if tokens >= 1_000_000 {
		tenths := (tokens*10 + 500_000) / 1_000_000
		if tenths%10 == 0 {
			return fmt.Sprintf("%dM context", tenths/10)
		}
		return fmt.Sprintf("%d.%dM context", tenths/10, tenths%10)
	}
	if tokens >= 1_000 {
		tenths := (tokens*10 + 500) / 1_000
		if tenths%10 == 0 {
			return fmt.Sprintf("%dK context", tenths/10)
		}
		return fmt.Sprintf("%d.%dK context", tenths/10, tenths%10)
	}
	return fmt.Sprintf("%d context", tokens)
}

func (m *model) renderListModal(title string, w int) string {
	var b strings.Builder
	writeAccent(&b, titleStyle.Render(title))
	if m.overlay == overlayResume {
		writeAccent(&b, dimStyle.Render("source: "+m.resumeFilter.label()))
	}
	if m.filter != "" {
		writeAccent(&b, dimStyle.Render("filter: "+m.filter))
	}
	writeAccent(&b, "")
	if len(m.items) == 0 {
		writeAccent(&b, dimStyle.Render("(empty)"))
	}
	start := 0
	limit := 16
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	for i, it := range m.items {
		if i < start || i >= start+limit {
			continue
		}
		if it.Header {
			writeAccent(&b, titleStyle.Render(it.Label))
			continue
		}
		line := it.Label
		if it.Detail != "" {
			line += "  " + dimStyle.Render(it.Detail)
		}
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		} else {
			line = "  " + line
		}
		writeAccent(&b, line)
	}
	writeAccent(&b, "")
	hint := "↑↓  enter  ·  esc close  ·  type to filter"
	if m.overlay == overlayResume {
		if m.resumePending != "" {
			hint = resumePendingHint
		} else {
			hint = resumePickerHint
		}
	}
	writeAccent(&b, dimStyle.Render(hint))
	return modalBox.Width(w).Render(b.String())
}

type confirmOption struct {
	key, label, desc string
}

func (m *model) openConfirm(kind string) {
	m.confirmKind = kind
	m.confirmCursor = 0
	m.overlay = overlayConfirm
}

func (m *model) confirmContent() (title, preview string, opts []confirmOption) {
	switch {
	case strings.HasPrefix(m.confirmKind, "doctor-fix:"):
		preview = doctorFixPreview(m.confirmKind)
		return doctorFixQuestion, preview, []confirmOption{
			{key: "y", label: "Apply", desc: doctorFixApplyDesc},
			{key: "n", label: "Cancel", desc: doctorFixCancelDesc},
		}
	case m.confirmKind == "rewind":
		return "Rewind the last exchange?", "", []confirmOption{
			{key: "y", label: "Yes", desc: ""},
			{key: "n", label: "No", desc: ""},
		}
	default:
		return deleteSessionQuestion, "", []confirmOption{
			{key: "y", label: deleteSessionApplyLabel, desc: deleteSessionApplyDesc},
			{key: "n", label: "Cancel", desc: deleteSessionCancelDesc},
		}
	}
}

func (m *model) renderConfirm(w int) string {
	title, preview, opts := m.confirmContent()
	var b strings.Builder
	writeAccent(&b, titleStyle.Render(title))
	writeAccent(&b, "")
	if preview != "" {
		lines := strings.Split(preview, "\n")
		if len(lines) > 12 {
			lines = append(lines[:12], "…")
		}
		for _, line := range lines {
			writeAccent(&b, dimStyle.Render(line))
		}
		writeAccent(&b, "")
	}
	for i, o := range opts {
		writeAccent(&b, radioRow(o.key, o.label, i == m.confirmCursor))
		if o.desc != "" {
			writeAccent(&b, dimStyle.Render("    "+o.desc))
		}
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) renderPalette(w int) string {
	hits := FuzzySlash(m.filter, m.screenMd)
	var b strings.Builder
	b.WriteString(titleStyle.Render("Commands") + dimStyle.Render("  "+m.filter) + "\n\n")
	if m.cursor >= len(hits) {
		m.cursor = 0
	}
	limit := 12
	for i, c := range hits {
		if i >= limit {
			break
		}
		line := "/" + c.Name + "  " + dimStyle.Render(c.Help)
		if i == m.cursor {
			line = selStyle.Render("/" + c.Name + "  " + c.Help)
		}
		b.WriteString(line + "\n")
	}
	if len(hits) == 0 {
		b.WriteString(dimStyle.Render("(no matches)") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("tab/enter run  ·  esc"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handlePaletteKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	rows := m.slashDropdownRows()
	if m.cursor >= len(rows) {
		m.cursor = 0
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	d := parseSlashDraft(m.ta.Value())
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		m.syncPromptPrefix()
		return nil, true
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		if d.InArgs && d.Cmd.Name == "theme" && m.cursor < len(rows) {
			applyTheme(resolveTheme(rows[m.cursor].Label))
		}
		return nil, true
	case "down", "tab":
		if msg.String() == "tab" && len(rows) > 0 {
			if m.cursor >= len(rows) {
				m.cursor = 0
			}
			m.ta.SetValue(rows[m.cursor].Insert)
			m.filter = m.ta.Value()
			m.cursor = 0
			return nil, true
		}
		if m.cursor+1 < len(rows) {
			m.cursor++
		}
		if d.InArgs && d.Cmd.Name == "theme" && m.cursor < len(rows) {
			applyTheme(resolveTheme(rows[m.cursor].Label))
		}
		return nil, true
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		m.ta.Reset()
		m.overlay = overlayNone
		m.syncPromptPrefix()
		if d.InArgs && d.OK {
			if strings.TrimSpace(d.Args) != "" {
				return m.handleSlash(text), true
			}
			if len(rows) == 0 {
				return m.handleSlash(text), true
			}
			run := rows[m.cursor].Run
			if run == "" {
				run = strings.TrimSpace(rows[m.cursor].Insert)
			}
			return m.handleSlash(run), true
		}
		if len(rows) == 0 {
			return m.handleSlash(text), true
		}
		pick := rows[m.cursor]
		if strings.HasPrefix(strings.ToLower(text), strings.ToLower(pick.Run)) && text != pick.Run {
			return m.handleSlash(text), true
		}
		return m.handleSlash(pick.Run), true
	}
	return nil, false
}

func (m *model) handleOverlayKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.overlay == overlayLogin {
		return m.handleLoginKey(msg), true
	}
	if m.overlay == overlaySettings {
		return m.handleSettingsKey(msg), true
	}
	if m.overlay == overlayDoctor && len(m.items) == 0 {
		switch msg.String() {
		case "esc":
			m.overlay = overlayNone
			return nil, true
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return nil, true
		case "down", "j":
			m.cursor++
			return nil, true
		}
		return nil, true
	}
	if m.overlay == overlayCache {
		if msg.String() == "esc" {
			m.overlay = overlayNone
		}
		return nil, true
	}
	if m.overlay == overlayWorkflows {
		return m.handleWorkflowsKey(msg), true
	}
	if m.overlay == overlayMemory {
		return m.handleMemoryKey(msg), true
	}
	if m.overlay == overlayAgents {
		return m.handleAgentsKey(msg), true
	}
	if m.overlay == overlayLineView {
		return m.handleLineViewerKey(msg), true
	}
	if m.overlay == overlayWorktree {
		return m.handleWorktreeKey(msg), true
	}
	if m.overlay == overlayImportClaude {
		return m.handleImportClaudeKey(msg), true
	}
	if m.overlay == overlayRewind {
		return m.handleRewindKey(msg), true
	}
	if m.overlay == overlayBtw {
		return m.handleBtwComposerKey(msg), true
	}
	if m.overlay == overlayBlock {
		return m.handleBlockViewerKey(msg), true
	}
	if m.overlay == overlayQueue {
		if cmd, handled := m.handleQueueKey(msg); handled {
			return cmd, true
		}
	}
	if m.overlay == overlayResume && m.resumePending != "" {
		switch msg.String() {
		case "y":
			id := m.resumePending
			m.resumePending = ""
			m.confirmDelete(id)
			m.fillSessions("resume")
			return nil, true
		case "n":
			m.resumePending = ""
			return nil, true
		case "esc":
			m.resumePending = ""
			m.overlay = overlayNone
			m.filter = ""
			return nil, true
		default:
			m.resumePending = ""
		}
	}
	if m.overlay == overlayResume && msg.String() == "d" {
		if m.cursor < len(m.items) {
			it := m.items[m.cursor]
			if strings.HasPrefix(it.Key, "resume:") {
				m.resumePending = strings.TrimPrefix(it.Key, "resume:")
			}
		}
		return nil, true
	}
	if m.overlay == overlayResume && msg.String() == "f" {
		m.resumeFilter = m.resumeFilter.next()
		saved := m.filter
		m.fillSessions("resume")
		if saved != "" {
			m.filter = saved
			m.applyOverlayFilter()
		}
		return nil, true
	}
	switch m.overlay {
	case overlayFeedback:
		switch msg.Type {
		case tea.KeyEsc:
			m.overlay = overlayNone
			m.feedback = ""
		case tea.KeyCtrlC:
			if strings.TrimSpace(m.feedback) != "" {
				m.feedback = ""
				return nil, true
			}
			m.overlay = overlayNone
		case tea.KeyEnter:
			text := m.feedback
			m.feedback = ""
			m.overlay = overlayNone
			m.sendFeedback(text)
		case tea.KeyBackspace:
			if len(m.feedback) > 0 {
				m.feedback = m.feedback[:len(m.feedback)-1]
			}
		case tea.KeyRunes:
			m.feedback += string(msg.Runes)
		}
		return nil, true
	case overlayConfirm:
		opts := 2
		switch msg.String() {
		case "up", "k":
			if m.confirmCursor > 0 {
				m.confirmCursor--
			}
			return nil, true
		case "down", "j":
			if m.confirmCursor+1 < opts {
				m.confirmCursor++
			}
			return nil, true
		case "y":
			m.confirmCursor = 0
			return m.applyConfirm(), true
		case "n", "esc":
			m.overlay = overlayNone
			m.confirmKind = ""
			return nil, true
		case "enter":
			return m.applyConfirm(), true
		}
		return nil, true
	}
	switch msg.String() {
	case "esc":
		if m.overlay == overlayTimeline || m.overlay == overlayFind {
			m.restoreJumpViewport()
		}
		if m.overlay == overlayTheme && m.themePreviewOrig != "" {
			m.theme = m.themePreviewOrig
			applyTheme(resolveTheme(m.themePreviewOrig))
			m.themePreviewOrig = ""
		}
		m.overlay = overlayNone
		m.filter = ""
		if m.screen == screenWelcome {
			m.refreshWelcome()
		}
		return nil, true
	case "up", "k":
		if m.overlay == overlayHelp || m.overlay == overlayResume {
			m.stepSelectableCursor(-1)
		} else if m.cursor > 0 {
			m.cursor--
		}
		m.previewOverlay()
		return nil, true
	case "down", "j":
		if m.overlay == overlayHelp || m.overlay == overlayResume {
			m.stepSelectableCursor(1)
		} else if m.cursor+1 < len(m.items) {
			m.cursor++
		}
		m.previewOverlay()
		return nil, true
	case "left", "h":
		if m.overlay == overlayExtensions {
			m.extTab = (m.extTab + 4) % 5
			m.fillExtensions()
		}
		return nil, true
	case "right", "l":
		if m.overlay == overlayExtensions {
			m.extTab = (m.extTab + 1) % 5
			m.fillExtensions()
		}
		return nil, true
	case "enter":
		return m.pickOverlay(), true
	}
	if msg.Type == tea.KeyRunes && m.overlay != overlayPrivacy && m.overlay != overlayTimeline && m.overlay != overlayResume {
		m.filter += string(msg.Runes)
		m.applyOverlayFilter()
		return nil, true
	}
	if m.overlay == overlayResume && msg.Type == tea.KeyRunes && msg.String() != "f" && msg.String() != "d" {
		m.filter += string(msg.Runes)
		m.applyOverlayFilter()
		return nil, true
	}
	if msg.Type == tea.KeyBackspace && m.filter != "" {
		m.filter = m.filter[:len(m.filter)-1]
		m.applyOverlayFilter()
		return nil, true
	}
	return nil, true
}

func (m *model) previewOverlay() {
	if m.overlay == overlayTheme && m.cursor < len(m.items) {
		applyTheme(resolveTheme(m.items[m.cursor].Key))
	}
	if m.overlay == overlayTimeline {
		m.previewJump()
	}
	if m.overlay == overlayFind {
		m.previewFind()
	}
}

func (m *model) applyOverlayFilter() {
	if m.overlay == overlayHelp {
		m.items = filterPaletteEntries(m.filter, m.paletteSrc)
		m.firstSelectableItem()
		return
	}
	if m.overlay == overlayFind {
		m.rebuildFindItems()
		m.previewFind()
		return
	}
	if m.overlay == overlayHistory {
		saved := m.filter
		m.items = fuzzyHistory(saved, m.history)
		if m.cursor >= len(m.items) {
			m.cursor = 0
		}
	}
	if m.overlay == overlayHistory {
		return
	}
	if m.filter == "" {
		return
	}
	f := strings.ToLower(strings.TrimSpace(m.filter))
	var keep []listItem
	for _, it := range m.items {
		blob := strings.ToLower(it.Key + " " + it.Label + " " + it.Detail)
		if strings.Contains(blob, f) {
			keep = append(keep, it)
		}
	}
	m.items = keep
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) applyConfirm() tea.Cmd {
	if m.confirmCursor != 0 {
		m.overlay = overlayNone
		m.confirmKind = ""
		return nil
	}
	kind := m.confirmKind
	m.overlay = overlayNone
	m.confirmKind = ""
	if strings.HasPrefix(kind, "delete-welcome:") {
		m.confirmDelete(strings.TrimPrefix(kind, "delete-welcome:"))
	} else if kind == "delete" && m.sess != nil {
		_ = agent.DeleteSession(m.sess)
		m.sess = nil
		m.screen = screenWelcome
		m.refreshWelcome()
	} else if kind == "rewind" {
		m.doRewind()
	} else if strings.HasPrefix(kind, "doctor-fix:") {
		m.applyDoctorFix(strings.TrimPrefix(kind, "doctor-fix:"))
	}
	return nil
}

func (m *model) pickOverlay() tea.Cmd {
	if m.cursor >= len(m.items) {
		return nil
	}
	it := m.items[m.cursor]
	switch m.overlay {
	case overlaySettings:
		if it.Header {
			return nil
		}
		return m.toggleSetting(it.Key)
	case overlayTheme:
		m.theme = it.Key
		applyTheme(resolveTheme(it.Key))
		saveTheme(it.Key)
		m.themePreviewOrig = ""
		m.overlay = overlayNone
		m.appendSys(saveThemeToast(canonicalThemeName(it.Key)))
	case overlayResume, overlayDashboard:
		if strings.HasPrefix(it.Key, "group:") {
			return nil
		}
		if strings.HasPrefix(it.Key, "foreign:") {
			m.appendSys("Foreign session " + it.Label + " — Entropy cannot replay another product's transcript. Start a new session and describe the work.")
			m.overlay = overlayNone
			return m.pickWelcome("new")
		}
		if strings.HasPrefix(it.Key, "resume:") {
			sess, err := agent.FindSession(strings.TrimPrefix(it.Key, "resume:"))
			if err != nil {
				sess, err = agent.LoadSession(m.opts.CWD, strings.TrimPrefix(it.Key, "resume:"))
			}
			if err == nil {
				cmd := m.attachSession(sess)
				m.layoutChat()
				m.overlay = overlayNone
				return cmd
			}
		} else if it.Key == "new" {
			m.overlay = overlayNone
			return m.handleNew()
		}
		m.overlay = overlayNone
	case overlayHistory:
		m.ta.SetValue(it.Key)
		m.overlay = overlayNone
		m.ta.Focus()
	case overlayModel:
		key := it.Key
		switch {
		case strings.HasPrefix(key, "recap:"):
			id := strings.TrimPrefix(key, "recap:")
			if id == "auto" {
				id = ""
			}
			m.cfg.UI.RecapModel = id
			m.persistUI()
			m.appendSys("recap model: " + emptyAuto(id))
		case strings.HasPrefix(key, "memory:"):
			id := strings.TrimPrefix(key, "memory:")
			if id == "auto" {
				id = ""
			}
			m.cfg.UI.MemoryModel = id
			m.persistUI()
			m.appendSys("memory model: " + emptyAuto(id))
		case strings.HasPrefix(key, "fork:"):
			id := strings.TrimPrefix(key, "fork:")
			if id == "auto" {
				id = ""
			}
			m.cfg.UI.ForkSecondary = id
			m.persistUI()
			m.appendSys("fork secondary model: " + emptyAuto(id))
		default:
			if mod, ok := catalog.Lookup(key); ok {
				m.opts.Model = mod
				if m.sess != nil {
					m.sess.ModelID = mod.ID
					m.sess.Provider = mod.Provider
					m.sess.Backend = mod.APIBackend
					m.sess.CloseExportBoundary(mod.Provider)
					_ = m.sess.Persist()
				}
				m.appendSys("model: " + mod.Name + " via " + mod.Provider.DisplayName())
			}
		}
		m.overlay = overlayNone
	case overlayDocs, overlayTutorial:
		m.appendSys(it.Detail + "\n\n" + it.Key)
		m.overlay = overlayNone
		m.refreshVP()
	case overlayPrivacy:
		m.cfg.UI.PrivacyOptOut = it.Key == "opt-out"
		m.ackPrivacyBanner(it.Key == "opt-in")
		_ = config.Save(m.cfg)
		m.overlay = overlayNone
		m.appendSys("privacy: " + it.Key)
	case overlayExtensions:
		if strings.HasPrefix(it.Key, "hint") {
			return nil
		}
		m.appendSys(it.Detail)
	case overlayDoctor:
		if it.Key != "" {
			m.openConfirm("doctor-fix:" + it.Key)
		}
		return nil
	case overlayFind:
		m.commitFind()
	case overlayHelp:
		return m.runPaletteItem(it)
	case overlayTimeline:
		m.commitJump()
	default:
		m.overlay = overlayNone
	}
	return nil
}

func (m *model) appendSys(s string) {
	if m.sess == nil {
		m.status = s
		return
	}
	m.appendBlock(block{kind: blockSystem, body: s})
	m.refreshVP()
}

func emptyAuto(s string) string {
	if s == "" {
		return "automatic"
	}
	return s
}

func (m *model) openAuxModel(which string) {
	m.overlay = overlayModel
	m.cursor = 0
	m.items = []listItem{{Key: which + ":auto", Label: "Automatic", Detail: "provider-aware default"}}
	for _, mod := range catalog.All() {
		m.items = append(m.items, listItem{
			Key: which + ":" + mod.ID, Label: mod.Name, Detail: string(mod.Provider),
		})
	}
}

func (m *model) openTheme() {
	m.overlay = overlayTheme
	m.cursor = 0
	m.themePreviewOrig = m.theme
	m.items = nil
	autoActive := ""
	if canonicalThemeName(m.theme) == "auto" {
		autoActive = " (active)"
	}
	m.items = append(m.items, listItem{
		Key:    "auto",
		Label:  "auto",
		Detail: "auto (follow system)" + autoActive,
	})
	current := canonicalThemeName(m.theme)
	for _, name := range themeCanonicalNames() {
		active := ""
		if name == current && current != "auto" {
			active = " (active)"
		}
		m.items = append(m.items, listItem{
			Key:    name,
			Label:  name,
			Detail: themePrettyName(name) + active,
		})
	}
	for i, it := range m.items {
		if it.Key == current || (current == "auto" && it.Key == "auto") {
			m.cursor = i
			break
		}
	}
	m.previewOverlay()
}

func (m *model) openResume() {
	m.overlay = overlayResume
	m.resumePending = ""
	m.filter = ""
	m.fillSessions("resume")
}

type resumeSourceFilter int

const (
	resumeFilterGrok resumeSourceFilter = iota
	resumeFilterExternal
	resumeFilterAll
	resumeFilterLocal
	resumeFilterRemote
)

func (f resumeSourceFilter) label() string {
	switch f {
	case resumeFilterExternal:
		return "External"
	case resumeFilterAll:
		return "All"
	case resumeFilterLocal:
		return "Local"
	case resumeFilterRemote:
		return "Remote"
	default:
		return "Grok"
	}
}

func (f resumeSourceFilter) next() resumeSourceFilter {
	switch f {
	case resumeFilterGrok:
		return resumeFilterExternal
	case resumeFilterExternal:
		return resumeFilterAll
	case resumeFilterAll:
		return resumeFilterLocal
	case resumeFilterLocal:
		return resumeFilterRemote
	default:
		return resumeFilterGrok
	}
}

func (f resumeSourceFilter) matches(source string) bool {
	foreign := isForeignPickerSource(source)
	switch f {
	case resumeFilterGrok:
		return !foreign
	case resumeFilterLocal:
		return source == "local" || source == "both"
	case resumeFilterRemote:
		return source == "remote" || source == "both" || source == "conversation"
	case resumeFilterExternal:
		return foreign
	default:
		return true
	}
}

func isForeignPickerSource(source string) bool {
	switch strings.ToLower(source) {
	case "claude", "codex", "cursor":
		return true
	default:
		return false
	}
}

func (m *model) fillSessions(kind string) {
	m.cursor = 0
	m.items = []listItem{{Key: "new", Label: "New session", Detail: m.dashCWD}}
	list, _ := agent.ListAllSessions()
	currentRepo := repoName(m.opts.CWD)
	groups := map[string][]agent.Session{}
	var order []string
	seen := map[string]bool{}
	for _, s := range list {
		repo := repoName(s.CWD)
		if !seen[repo] {
			seen[repo] = true
			order = append(order, repo)
		}
		groups[repo] = append(groups[repo], s)
	}
	if currentRepo != "" {
		for i, r := range order {
			if r == currentRepo {
				order = append([]string{r}, append(order[:i], order[i+1:]...)...)
				break
			}
		}
	}
	for _, repo := range order {
		var rows []listItem
		for _, s := range groups[repo] {
			if !m.resumeFilter.matches("local") {
				continue
			}
			title := s.Title
			if title == "" {
				title = "(untitled)"
			}
			rows = append(rows, listItem{
				Key:    "resume:" + s.ID,
				Label:  "  " + shortID(s.ID) + "  " + title,
				Detail: s.UpdatedAt.Format("15:04") + "  " + s.ModelID + "  " + s.CWD,
			})
		}
		if len(rows) == 0 {
			continue
		}
		m.items = append(m.items, listItem{Key: "group:" + repo, Label: repo, Header: true})
		m.items = append(m.items, rows...)
	}
	for _, f := range foreign.ScanRecent(m.opts.CWD) {
		src := strings.ToLower(f.Tool)
		if !m.resumeFilter.matches(src) {
			continue
		}
		m.items = append(m.items, listItem{
			Key:    "foreign:" + f.Tool + ":" + f.ID,
			Label:  "[" + f.Tool + "] " + f.Title,
			Detail: f.UpdatedAt.Format("15:04") + "  " + f.CWD,
		})
	}
	m.firstSelectableItem()
	_ = kind
}

func repoName(cwd string) string {
	cwd = filepath.Clean(cwd)
	base := filepath.Base(cwd)
	parent := filepath.Base(filepath.Dir(cwd))
	if parent == "." || parent == "/" || parent == string(filepath.Separator) {
		return base
	}
	return parent + "-" + base
}

func (m *model) openHistory() {
	m.overlay = overlayHistory
	m.cursor = 0
	m.ta.Reset()
	m.syncPromptPrefix()
	m.refreshHistorySearch()
}

func (m *model) refreshHistorySearch() {
	if m.overlay != overlayHistory {
		return
	}
	q := m.ta.Value()
	m.filter = q
	m.items = fuzzyHistory(q, m.history)
	if m.cursor >= len(m.items) {
		m.cursor = 0
	}
}

func (m *model) handleHistoryKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		m.ta.Reset()
		m.syncPromptPrefix()
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
	case "enter", "tab":
		if m.cursor < len(m.items) {
			m.ta.SetValue(m.items[m.cursor].Key)
		}
		m.overlay = overlayNone
		m.syncPromptPrefix()
		m.ta.Focus()
		return nil, true
	}
	return nil, false
}

func fuzzyHistory(q string, hist []string) []listItem {
	var items []listItem
	for i := len(hist) - 1; i >= 0; i-- {
		items = append(items, listItem{Key: hist[i], Label: truncate(hist[i], 70)})
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return items
	}
	type scored struct {
		it listItem
		n  int
	}
	var hits []scored
	for _, it := range items {
		n, _ := nucleoMatch(q, it.Key)
		if n == 0 {
			continue
		}
		_, marks := nucleoMatch(q, it.Label)
		it.Marks = marks
		hits = append(hits, scored{it, n})
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].n > hits[i].n {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	out := make([]listItem, len(hits))
	for i, h := range hits {
		out[i] = h.it
	}
	return out
}

func (m *model) openModels() {
	m.overlay = overlayModel
	m.cursor = 0
	m.items = nil
	mods := catalog.All()
	sort.SliceStable(mods, func(i, j int) bool {
		pi := mods[i].Provider.DisplayName()
		pj := mods[j].Provider.DisplayName()
		if !strings.EqualFold(pi, pj) {
			return strings.ToLower(pi) < strings.ToLower(pj)
		}
		return strings.ToLower(mods[i].Name) < strings.ToLower(mods[j].Name)
	})
	for _, mod := range mods {
		label := mod.Provider.DisplayName() + " · " + mod.Name
		if m.opts.Model.ID == mod.ID {
			label += " (current)"
		}
		m.items = append(m.items, listItem{
			Key:    mod.ID,
			Label:  label,
			Detail: modelPickerDetail(mod),
		})
		if m.opts.Model.ID == mod.ID {
			m.cursor = len(m.items) - 1
		}
	}
}

func (m *model) openDocs() {
	m.overlay = overlayDocs
	m.cursor = 0
	m.items = docsItems()
}

func (m *model) openTutorial() {
	if m.screenMd == modeMinimal {
		m.appendSys("/tutorial isn't available in minimal mode. Run /fullscreen.")
		return
	}
	m.overlay = overlayTutorial
	m.cursor = 0
	m.items = tutorialItems()
}

func (m *model) fillExtensions() {
	m.cursor = 0
	m.items = nil
	cwd := m.opts.CWD
	switch m.extTab {
	case 0:
		for _, h := range hooks.List(cwd) {
			m.items = append(m.items, listItem{Label: filepath.Base(h), Detail: h})
		}
		for _, h := range hooks.ListImported(cwd) {
			m.items = append(m.items, listItem{
				Label:  h.Event + " · " + h.Matcher,
				Detail: "imported  " + truncate(h.Command, 48),
			})
		}
		if len(m.items) == 0 {
			m.items = append(m.items, listItem{Label: "(no hooks)", Detail: filepath.Join(cwd, ".entropy", "hooks")})
		}
	case 1:
		m.items = dirListing(append([]string{filepath.Join(config.Home(), "plugins")}, config.ProjectSubdir(cwd, "plugins")...)...)
	case 2:
		m.fillMarketplace()
	case 3:
		for _, s := range tools.DiscoverSkills(cwd) {
			badge := "skill"
			if s.UserInvocable {
				badge = "skill · slash"
			}
			m.items = append(m.items, listItem{Label: s.Name, Detail: badge + "  " + truncate(s.Description, 40)})
		}
		if len(m.items) == 0 {
			m.items = append(m.items, listItem{Label: "(no skills)"})
		}
	default:
		cfg, _ := config.Load()
		for name, s := range cfg.MCP {
			m.items = append(m.items, listItem{Label: name, Detail: s.Command + " " + strings.Join(s.Args, " ")})
		}
		if len(m.items) == 0 {
			m.items = append(m.items, listItem{Label: "(no mcp_servers in config.toml)"})
		}
	}
}

func (m *model) openMCP() {
	m.overlay = overlayMCP
	m.fillMCP()
}

func (m *model) fillMCP() {
	m.cursor = 0
	m.items = nil
	cfg, _ := config.Load()
	n := len(cfg.MCP)
	m.items = append(m.items, listItem{
		Key:    "section:managed",
		Label:  "Managed by grok.com (0)",
		Detail: "xAI-hosted connectors — not used by Entropy; local [mcp_servers] below",
	})
	m.items = append(m.items, listItem{
		Key:    "section:local",
		Label:  fmt.Sprintf("Local (%d)", n),
		Detail: "config.toml [mcp_servers]",
	})
	for name, s := range cfg.MCP {
		detail := s.Command + " " + strings.Join(s.Args, " ")
		if s.URL != "" {
			detail = s.URL
		}
		status := "configured"
		if m.sess != nil {
			found := false
			for _, t := range m.sess.MCPTools {
				if strings.Contains(t.Name(), sanitizeMCPName(name)) {
					found = true
					break
				}
			}
			if found {
				status = "connected"
			} else if m.sess.MCPClients != nil {
				status = "offline"
			}
		}
		m.items = append(m.items, listItem{
			Key:    "mcp:" + name,
			Label:  name + "  ·  " + status,
			Detail: strings.TrimSpace(detail),
		})
	}
	if n == 0 {
		m.items = append(m.items, listItem{Label: "(no mcp_servers in config.toml)", Detail: "add [mcp_servers.<name>] command / url"})
	}
	if m.sess != nil {
		for _, t := range m.sess.MCPTools {
			m.items = append(m.items, listItem{
				Key:    "tool:" + t.Name(),
				Label:  t.Name(),
				Detail: "live tool  " + string(t.Access()),
			})
		}
	}
}

func sanitizeMCPName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (m *model) fillMarketplace() {
	home := config.Home()
	cwd := m.opts.CWD
	m.items = []listItem{{
		Key:    "hint",
		Label:  "Local catalog (no grok.com store)",
		Detail: "Entropy cannot reach xAI marketplace. Drop a plugin into " + filepath.Join(home, "plugins") + " or a skill into " + filepath.Join(home, "skills"),
	}}
	plugins := dirListing(append([]string{filepath.Join(home, "plugins")}, config.ProjectSubdir(cwd, "plugins")...)...)
	for _, it := range plugins {
		if it.Label == "(empty)" {
			continue
		}
		m.items = append(m.items, listItem{Key: "plugin:" + it.Label, Label: "plugin · " + it.Label, Detail: it.Detail})
	}
	for _, s := range tools.DiscoverSkills(cwd) {
		m.items = append(m.items, listItem{Key: "skill:" + s.Name, Label: "skill · " + s.Name, Detail: truncate(s.Description, 48)})
	}
	for _, it := range dirListing(filepath.Join(home, "marketplace")) {
		if it.Label == "(empty)" {
			continue
		}
		m.items = append(m.items, listItem{Key: "cache:" + it.Label, Label: "cache · " + it.Label, Detail: it.Detail})
	}
	if len(m.items) == 1 {
		m.items = append(m.items, listItem{
			Key:    "hint-install",
			Label:  "Install from a path",
			Detail: "Copy a plugin directory into ~/.entropy/plugins or a SKILL.md tree into ~/.entropy/skills, then reopen /marketplace",
		})
	}
}

func (m *model) openExtensions(tab int) {
	m.overlay = overlayExtensions
	m.extTab = tab
	m.fillExtensions()
}

func (m *model) openHelp() {
	if m.overlay == overlayHelp {
		m.overlay = overlayNone
		m.filter = ""
		return
	}
	m.overlay = overlayHelp
	m.filter = ""
	m.paletteSrc = defaultPaletteEntries(m.screenMd)
	m.items = m.paletteSrc
	m.firstSelectableItem()
}

func dirListing(paths ...string) []listItem {
	var out []listItem
	for _, p := range paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			out = append(out, listItem{Label: e.Name(), Detail: p})
		}
	}
	if len(out) == 0 {
		out = append(out, listItem{Label: "(none on disk)", Detail: strings.Join(paths, " · ")})
	}
	return out
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func isTTY(fd int) bool {
	st, err := os.Stat("/dev/tty")
	_ = fd
	return err == nil && st != nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
