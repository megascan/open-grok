package pager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/hooks"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

func (m *model) viewWithOverlay(base string) string {
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
	case overlayPlan:
		return m.renderPlanApproval(w)
	case overlayCancelTurn:
		return m.renderCancelTurn(w)
	case overlayFiles:
		return m.renderListModal("@ files", w)
	case overlayPalette:
		return m.renderPalette(w)
	case overlayPermission:
		return m.renderPermission(w)
	case overlayConfirm:
		return modalBox.Width(w).Render(titleStyle.Render("Confirm") + "\n\nDelete this session?  y yes  ·  n cancel")
	case overlayFeedback:
		return modalBox.Width(w).Render(titleStyle.Render("Feedback") + "\n\n" + m.feedback + "█\n\n" + dimStyle.Render("enter send  ·  esc discard"))
	case overlaySettings:
		return m.renderListModal("Settings", w)
	case overlayTheme:
		return m.renderListModal("Theme (live preview)", w)
	case overlayDashboard, overlayResume, overlayHistory, overlayModel,
		overlayDoctor, overlayDocs, overlayTutorial, overlayExtensions,
		overlayWorkflows, overlayPersonas, overlayAgents, overlayFind,
		overlayHelp, overlayPrivacy, overlayAnnouncements, overlayReleaseNotes,
		overlayUsage, overlayQueue, overlayTasks, overlayCache, overlayImportClaude,
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
		return "Slash commands"
	case overlayPrivacy:
		return "Privacy"
	case overlayAnnouncements:
		return "Announcements"
	case overlayReleaseNotes:
		return "Release notes"
	case overlayUsage:
		return "Usage"
	case overlayQueue:
		return "Follow-up queue"
	case overlayTasks:
		return "Background tasks"
	case overlayCache:
		return "Prompt cache"
	case overlayImportClaude:
		return "Import Claude settings"
	case overlayMCP:
		return "MCP servers"
	case overlayTimeline:
		return "Timeline"
	}
	return "Entropy"
}

func (m *model) renderListModal(title string, w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n")
	if m.filter != "" {
		b.WriteString(dimStyle.Render("filter: "+m.filter) + "\n")
	}
	b.WriteByte('\n')
	if len(m.items) == 0 {
		b.WriteString(dimStyle.Render("(empty)") + "\n")
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
		line := it.Label
		if it.Detail != "" {
			line += "  " + dimStyle.Render(it.Detail)
		}
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("↑↓  enter  ·  esc close  ·  type to filter"))
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
	hits := FuzzySlash(m.filter, m.screenMd)
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		return nil, true
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return nil, true
	case "down", "tab":
		if msg.String() == "tab" && len(hits) > 0 {
			pick := hits[m.cursor]
			m.ta.SetValue("/" + pick.Name + " ")
			m.filter = m.ta.Value()
			return nil, true
		}
		if m.cursor+1 < len(hits) {
			m.cursor++
		}
		return nil, true
	case "enter":
		if len(hits) == 0 {
			m.overlay = overlayNone
			return nil, true
		}
		pick := hits[m.cursor]
		arg := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(m.ta.Value()), "/"+strings.ToLower(pick.Name)))
		// If the user typed a full command line, dispatch that; else run the highlight.
		text := strings.TrimSpace(m.ta.Value())
		m.ta.Reset()
		m.overlay = overlayNone
		if strings.HasPrefix(strings.ToLower(text), "/"+pick.Name) && arg != strings.TrimSpace(text) {
			return m.handleSlash(text), true
		}
		return m.handleSlash("/" + pick.Name + " " + arg), true
	}
	return nil, false
}

func (m *model) handleOverlayKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch m.overlay {
	case overlayFeedback:
		switch msg.Type {
		case tea.KeyEsc:
			m.overlay = overlayNone
			m.feedback = ""
		case tea.KeyEnter:
			m.appendBlock(block{kind: blockSystem, body: "feedback recorded locally (not uploaded):\n" + m.feedback})
			m.feedback = ""
			m.overlay = overlayNone
			m.refreshVP()
		case tea.KeyBackspace:
			if len(m.feedback) > 0 {
				m.feedback = m.feedback[:len(m.feedback)-1]
			}
		case tea.KeyRunes:
			m.feedback += string(msg.Runes)
		}
		return nil, true
	case overlayConfirm:
		switch msg.String() {
		case "y", "enter":
			kind := m.confirmKind
			m.overlay = overlayNone
			m.confirmKind = ""
			if strings.HasPrefix(kind, "delete-welcome:") {
				m.confirmDelete(strings.TrimPrefix(kind, "delete-welcome:"))
			} else if kind == "delete" && m.sess != nil {
				id := m.sess.ID
				_ = agent.DeleteSession(m.sess)
				m.sess = nil
				m.screen = screenWelcome
				m.refreshWelcome()
				_ = id
			}
		case "n", "esc":
			m.overlay = overlayNone
		}
		return nil, true
	}
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		m.filter = ""
		if m.screen == screenWelcome {
			m.refreshWelcome()
		}
		return nil, true
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.previewOverlay()
		return nil, true
	case "down", "j":
		if m.cursor+1 < len(m.items) {
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
	if msg.Type == tea.KeyRunes && m.overlay != overlayPrivacy {
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
}

func (m *model) applyOverlayFilter() {
	if m.overlay == overlaySettings {
		saved := m.cursor
		m.fillSettings()
		m.cursor = saved
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

func (m *model) pickOverlay() tea.Cmd {
	if m.cursor >= len(m.items) {
		return nil
	}
	it := m.items[m.cursor]
	switch m.overlay {
	case overlaySettings:
		return m.toggleSetting(it.Key)
	case overlayTheme:
		m.theme = it.Key
		applyTheme(resolveTheme(it.Key))
		saveTheme(it.Key)
		m.overlay = overlayNone
		m.appendSys("theme: " + it.Key)
	case overlayResume, overlayDashboard:
		if strings.HasPrefix(it.Key, "resume:") {
			sess, err := agent.LoadSession(m.opts.CWD, strings.TrimPrefix(it.Key, "resume:"))
			if err == nil {
				m.attachSession(sess)
				m.layoutChat()
			}
		} else if it.Key == "new" {
			sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, m.permMode == permAlways)
			if err == nil {
				m.attachSession(sess)
				m.layoutChat()
			}
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
		_ = config.Save(m.cfg)
		m.overlay = overlayNone
		m.appendSys("privacy: " + it.Key)
	case overlayFind:
		m.jumpFind(it.Key)
		m.overlay = overlayNone
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
	m.items = nil
	for _, t := range builtinThemes {
		m.items = append(m.items, listItem{Key: t.Name, Label: t.Name, Detail: strings.Join(t.Aliases, ", ")})
	}
	m.items = append(m.items, listItem{Key: "auto", Label: "auto", Detail: "follow system appearance"})
}

func (m *model) openResume() {
	m.overlay = overlayResume
	m.fillSessions("resume")
}

func (m *model) fillSessions(kind string) {
	m.cursor = 0
	m.items = []listItem{{Key: "new", Label: "New session", Detail: m.dashCWD}}
	list, _ := agent.ListSessions(m.opts.CWD)
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		m.items = append(m.items, listItem{
			Key: "resume:" + s.ID, Label: shortID(s.ID) + "  " + title,
			Detail: s.UpdatedAt.Format("15:04") + "  " + s.ModelID,
		})
	}
	_ = kind
}

func (m *model) openHistory() {
	m.overlay = overlayHistory
	m.cursor = 0
	m.items = nil
	for i := len(m.history) - 1; i >= 0; i-- {
		m.items = append(m.items, listItem{Key: m.history[i], Label: truncate(m.history[i], 60)})
	}
}

func (m *model) openModels() {
	m.overlay = overlayModel
	m.cursor = 0
	m.items = nil
	for _, mod := range catalog.All() {
		m.items = append(m.items, listItem{
			Key: mod.ID, Label: mod.Name, Detail: string(mod.Provider) + "  " + string(mod.APIBackend),
		})
	}
}

func (m *model) openDoctor() {
	m.overlay = overlayDoctor
	m.cursor = 0
	home := config.Home()
	items := []listItem{
		{Label: "version", Detail: version.String()},
		{Label: "home", Detail: home},
		{Label: "never ~/.grok", Detail: onOff(!config.NeverGrokHome(home))},
		{Label: "tty", Detail: fmt.Sprintf("stdin=%v stdout=%v", isTTY(0), isTTY(1))},
		{Label: "color", Detail: "lipgloss / 256-color + truecolor themes"},
		{Label: "clipboard", Detail: "OSC 52 + ~/.entropy/last-copy.txt backup"},
		{Label: "sandbox", Detail: onOff(sandboxActive())},
	}
	st := auth.Status()
	for _, p := range types.AllProviders {
		mark := "signed out"
		if s, ok := st[p]; ok {
			mark = s
		}
		items = append(items, listItem{Label: p.DisplayName(), Detail: mark})
	}
	m.items = items
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
		if len(m.items) == 0 {
			m.items = append(m.items, listItem{Label: "(no hooks)", Detail: filepath.Join(cwd, ".entropy", "hooks")})
		}
	case 1:
		m.items = dirListing(append([]string{filepath.Join(config.Home(), "plugins")}, config.ProjectSubdir(cwd, "plugins")...)...)
	case 2:
		m.items = dirListing(filepath.Join(config.Home(), "marketplace"))
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

func (m *model) openExtensions(tab int) {
	m.overlay = overlayExtensions
	m.extTab = tab
	m.fillExtensions()
}

func (m *model) openHelp() {
	m.overlay = overlayHelp
	m.cursor = 0
	m.items = nil
	for _, c := range visibleCatalog(m.screenMd, false) {
		al := ""
		if len(c.Aliases) > 0 {
			al = strings.Join(c.Aliases, ", ")
		}
		m.items = append(m.items, listItem{Label: "/" + c.Name, Detail: c.Help + "  " + al})
	}
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
