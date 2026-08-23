package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
)

func TestFormatMemoryModified(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if formatMemoryModified(time.Time{}, now) != "unknown" {
		t.Fatal("zero")
	}
	if formatMemoryModified(now, now) != "just now" {
		t.Fatal("equal")
	}
	if formatMemoryModified(now.Add(-30*time.Second), now) != "just now" {
		t.Fatal("30s")
	}
	if formatMemoryModified(now.Add(-2*time.Minute), now) != "2m ago" {
		t.Fatal("2m")
	}
	if formatMemoryModified(now.Add(-2*time.Hour), now) != "2h ago" {
		t.Fatal("2h")
	}
	if formatMemoryModified(now.Add(-48*time.Hour), now) != "2d ago" {
		t.Fatal("2d")
	}
}

func TestMemoryFileLabel(t *testing.T) {
	if memoryFileLabel("/home/user/.entropy/memory/MEMORY.md") != "MEMORY.md" {
		t.Fatal("global")
	}
	if memoryFileLabel("/ws/.entropy/memory/sessions/2026-01-15-fix-bug.md") != "2026-01-15-fix-bug.md" {
		t.Fatal("session")
	}
}

func TestBuildMemoryEntriesGroupsBySource(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	entries := buildMemoryEntries([]memoryFileInfo{
		{Path: "/global/MEMORY.md", Source: "global", Size: 100, Modified: now},
		{Path: "/workspace/MEMORY.md", Source: "workspace", Size: 200, Modified: now},
		{Path: "/sessions/log1.md", Source: "session", Size: 50},
	}, now)
	if len(entries) != 6 {
		t.Fatalf("len %d", len(entries))
	}
	if !entries[0].Header || entries[0].Label != "Global" {
		t.Fatalf("%+v", entries[0])
	}
	if entries[1].Header || entries[1].Label != "MEMORY.md" {
		t.Fatalf("%+v", entries[1])
	}
	if !entries[2].Header || entries[2].Label != "Workspace" {
		t.Fatalf("%+v", entries[2])
	}
	if !entries[4].Header || entries[4].Label != "Sessions" {
		t.Fatalf("%+v", entries[4])
	}
}

func TestMemoryFilterPreservesSectionHeaders(t *testing.T) {
	entries := buildMemoryEntries([]memoryFileInfo{
		{Path: "/g/MEMORY.md", Source: "global", Size: 1, Modified: time.Now()},
		{Path: "/s/session-log.md", Source: "session", Size: 1, Modified: time.Now()},
	}, time.Now())
	idx := computeMemoryFiltered(entries, "memory")
	if len(idx) != 2 || !entries[idx[0]].Header || entries[idx[0]].Label != "Global" {
		t.Fatalf("%v %+v", idx, entries)
	}
	if entries[idx[1]].Label != "MEMORY.md" {
		t.Fatalf("%+v", entries[idx[1]])
	}
}

func TestMemoryDeleteOnlySessionLogs(t *testing.T) {
	m := testModel(t)
	m.mem = &memoryModal{
		entries: []memoryEntry{
			{Label: "Global", Header: true},
			{Label: "MEMORY.md", Source: "global", Path: "/tmp/x"},
			{Label: "Sessions", Header: true},
			{Label: "session-log.md", Source: "session", Path: "/tmp/y"},
		},
		selected: 1,
		mode:     memoryBrowse,
		enabled:  true,
	}
	m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.mem.mode != memoryBrowse {
		t.Fatal("global x must not arm delete")
	}
	m.mem.selected = 3
	m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.mem.mode != memoryConfirmDelete {
		t.Fatalf("mode %d", m.mem.mode)
	}
}

func TestMemorySlashAndIEnterFilter(t *testing.T) {
	m := testModel(t)
	m.mem = &memoryModal{mode: memoryBrowse, enabled: true}
	m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.mem.mode != memoryFilter {
		t.Fatal("/")
	}
	m.mem.mode = memoryBrowse
	m.handleMemoryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.mem.mode != memoryFilter {
		t.Fatal("i aliases /")
	}
}

func TestMemoryModalOpensWhenDisabled(t *testing.T) {
	m := testModel(t)
	m.cfg.Features.Memory = false
	m.runSlash("memory", "")
	if m.overlay != overlayMemory || m.mem == nil {
		t.Fatalf("overlay %d mem %v", m.overlay, m.mem)
	}
	if m.mem.enabled {
		t.Fatal("should reflect disabled")
	}
	body := m.renderMemory(90)
	if !strings.Contains(stripANSI(body), "Memory") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(stripANSI(body), "/ to filter") && !strings.Contains(stripANSI(body), "type to filter") {
		t.Fatalf("filter placeholder missing:\n%s", body)
	}
}

func TestAgentsTabsAndPersonaCreate(t *testing.T) {
	m := testModel(t)
	m.openAgents(agentsTabAgents)
	if m.overlay != overlayAgents {
		t.Fatal("overlay")
	}
	if len(m.agents.agents) < len(builtinAgents) {
		t.Fatalf("builtins %d", len(m.agents.agents))
	}
	body := m.renderAgents(72)
	if !strings.Contains(body, "Agents") || !strings.Contains(body, "Personas") {
		t.Fatalf("%s", body)
	}
	m.handleAgentsKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.agents.tab != agentsTabPersonas {
		t.Fatal("tab")
	}
	m.handleAgentsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.agents.create == nil {
		t.Fatal("create form")
	}
	m.agents.create.Name = "reviewer"
	m.agents.create.Desc = "code review"
	m.handlePersonaCreateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.agents.create != nil {
		t.Fatalf("err %v", m.agents.create.Err)
	}
	path := filepath.Join(config.Home(), "personas", "reviewer.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "code review") {
		t.Fatalf("%s", data)
	}
	if len(m.agents.personas) == 0 {
		t.Fatal("persona list")
	}
	m.agents.selP = 0
	m.handleAgentsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.agents.detail == nil || m.agents.detail.Name == "" {
		t.Fatalf("%+v", m.agents.detail)
	}
	got := m.renderPersonaDetail(72)
	if !strings.Contains(got, "persona:") || !strings.Contains(got, "Name") {
		t.Fatalf("%s", got)
	}
}

func TestCreatePersonaTemplateProjectScope(t *testing.T) {
	cwd := t.TempDir()
	path, err := createPersonaTemplate("scout", "explore", "look around", true, cwd)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, config.ProjectDirName(), "personas", "scout.toml")
	if path != want {
		t.Fatalf("%s vs %s", path, want)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "look around") {
		t.Fatalf("%s", data)
	}
}

func TestSanitizeConfigNameRequiresAlnum(t *testing.T) {
	if _, err := sanitizeConfigName("..."); err == nil {
		t.Fatal("expected error")
	}
	got, err := sanitizeConfigName("My Persona")
	if err != nil || got != "my-persona" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestLineRangeSuffixMatchesRust(t *testing.T) {
	if lineRangeSuffix(4, 4) != ":4" {
		t.Fatal(lineRangeSuffix(4, 4))
	}
	if lineRangeSuffix(1, 3) != ":1-3" {
		t.Fatal(lineRangeSuffix(1, 3))
	}
	s, e, ok := parseLineRange("10-12")
	if !ok || s != 10 || e != 12 {
		t.Fatalf("%d %d %v", s, e, ok)
	}
}

func TestLineViewerVisualRangeAndDraft(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	m.opts.CWD = dir
	path := filepath.Join(dir, "alpha.go")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.ta.SetValue("read @alpha.go")
	if cmd, ok := m.tryOpenLineViewerFromDraft(); !ok {
		t.Fatal("open")
	} else {
		_ = cmd
	}
	if m.overlay != overlayLineView || m.lineView == nil {
		t.Fatal("viewer")
	}
	if len(m.lineView.Lines) != 4 {
		t.Fatalf("lines %d %+v", len(m.lineView.Lines), m.lineView.Lines)
	}
	m.lineView.Visual = true
	m.lineView.Anchor = 0
	m.lineView.Cursor = 2
	if m.lineView.suffix() != ":1-3" {
		t.Fatalf("suffix %s", m.lineView.suffix())
	}
	m.confirmLineViewer()
	if got := m.ta.Value(); got != "read @alpha.go:1-3 " {
		t.Fatalf("draft %q", got)
	}
}

func TestFileSearchColonOpensLineViewer(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	m.opts.CWD = dir
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.ta.SetValue("see @al")
	m.refreshFileSearch()
	if m.overlay != overlayFiles || len(m.items) == 0 {
		t.Fatalf("dropdown %+v", m.items)
	}
	m.cursor = 0
	m.handleFileSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	if m.overlay != overlayLineView {
		t.Fatalf("overlay %d", m.overlay)
	}
}

func TestDashboardDetailsChromeFlushChips(t *testing.T) {
	m := testModel(t)
	m.width = 80
	m.dashDetails = true
	m.sess = &agent.Session{ID: "a", Title: "Add responsiveness to /context"}
	m.dashRows = []dashRow{
		{Sess: agent.Session{ID: "a"}, State: "idle"},
		{Sess: agent.Session{ID: "b"}, State: "idle"},
		{Sess: agent.Session{ID: "c"}, State: "idle"},
	}
	got := stripANSI(m.renderDashDetailsHeader())
	if !strings.Contains(got, "[Dashboard]") {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(got, "1/3") || !strings.Contains(got, "[‹][›]") {
		t.Fatalf("flush chips missing: %q", got)
	}
	if strings.Contains(got, "[‹] [›]") {
		t.Fatalf("chips must be flush without a space: %q", got)
	}
}

func TestImagineInstructionMatchesRust(t *testing.T) {
	got := imagineInstruction("a sunset")
	if !strings.Contains(got, "Call the image_gen tool immediately") {
		t.Fatalf("%s", got)
	}
	if !strings.Contains(got, "verbatim") || !strings.Contains(got, "Prompt: a sunset") {
		t.Fatalf("%s", got)
	}
	display, wire := expandSubmit("/imagine a sunset")
	if display != "/imagine a sunset" {
		t.Fatalf("display %q", display)
	}
	if wire != got {
		t.Fatalf("wire mismatch")
	}
	if !strings.Contains(imagineUsageMessage(), "Usage: /imagine <description>") {
		t.Fatal(imagineUsageMessage())
	}
	v := imagineVideoInstruction("orbit the city")
	if !strings.Contains(v, "# Imagine Video") || !strings.Contains(v, "User prompt: orbit the city") {
		t.Fatalf("%s", v)
	}
	if !strings.Contains(v, "`image_to_video`") {
		t.Fatal("skill must name image_to_video")
	}
}

func TestImagineSlashShowsUsageAndDisplay(t *testing.T) {
	m := testModel(t)
	m.runSlash("imagine", "")
	if !strings.Contains(m.status, "Usage: /imagine") && (len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].body, "Usage: /imagine")) {
		t.Fatalf("status %q blocks %+v", m.status, m.blocks)
	}
}

func TestVoiceOverlayToggleAndSend(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	m.runSlash("voice", "")
	if m.overlay != overlayVoice || m.voice == nil || !m.voice.Active {
		t.Fatalf("overlay %d voice %+v", m.overlay, m.voice)
	}
	body := stripANSI(m.renderVoiceBanner())
	if !strings.Contains(body, "Recording") {
		t.Fatalf("%s", body)
	}
	m.handleVoiceKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if m.voice.Transcript != "hello" {
		t.Fatalf("%q", m.voice.Transcript)
	}
	m.handleVoiceKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay == overlayVoice || (m.voice != nil && m.voice.Active) {
		t.Fatal("esc must cancel without sending")
	}
	m.startVoice()
	m.voice.Transcript = "typed dictation"
	cmd := m.handleVoiceKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter must submit")
	}
	if m.overlay == overlayVoice {
		t.Fatal("enter stops recording")
	}
}

func TestVoiceChordIgnoredOnWelcome(t *testing.T) {
	m := testModel(t)
	m.screen = screenWelcome
	m.toggleVoice()
	if m.voice != nil {
		t.Fatal("welcome has no dictation target")
	}
}

func TestWorkflowsEmptyAndLiveRun(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.openWorkflows()
	body := stripANSI(m.renderWorkflows(72))
	if !strings.Contains(body, "No workflow runs in this session yet.") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, "/ultracode") || !strings.Contains(body, "/deep-research") {
		t.Fatalf("%s", body)
	}
	m.startWorkflow("ultracode", "Ultracode", "port the pager", "wire")
	if len(m.workflows) != 1 || m.workflows[0].Status != "active" {
		t.Fatalf("%+v", m.workflows)
	}
	m.openWorkflows()
	list := stripANSI(m.renderWorkflows(72))
	if !strings.Contains(list, "Ultracode") || !strings.Contains(list, "0/4 phases") {
		t.Fatalf("%s", list)
	}
	m.handleWorkflowsKey(tea.KeyMsg{Type: tea.KeyEnter})
	detail := stripANSI(m.renderWorkflows(72))
	if !strings.Contains(detail, "Scout") || !strings.Contains(detail, "p pause") {
		t.Fatalf("%s", detail)
	}
	m.handleWorkflowsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.workflows[0].Status != "user_paused" {
		t.Fatalf("status %s", m.workflows[0].Status)
	}
	m.handleWorkflowsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.workflows[0].Status != "cancelled" {
		t.Fatalf("status %s", m.workflows[0].Status)
	}
}

func TestMCPAlwaysAllowToolVsServer(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	ch := make(chan bool, 1)
	m.perm = &permAskMsg{
		Tool: "mcp_linear_list_issues", Access: "mcp",
		Server: "linear", Reply: ch,
	}
	m.initPermCard()
	choices := m.permChoices()
	if choices[0].ID != "always" || !strings.Contains(choices[0].Label, "Always allow: (Linear) List Issues") {
		t.Fatalf("tool-scope %+v", choices)
	}
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyRight})
	choices = m.permChoices()
	if !strings.Contains(choices[0].Label, "Always allow: all tools from Linear") {
		t.Fatalf("server-scope %q", choices[0].Label)
	}
	if !m.permMCPServer {
		t.Fatal("right selects server")
	}
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyLeft})
	if m.permMCPServer {
		t.Fatal("left selects tool")
	}
	m.permMCPServer = true
	m.applyPermChoice(permChoice{ID: "always"})
	if !<-ch {
		t.Fatal("allow")
	}
	found := false
	for _, r := range m.sess.Allow {
		if r == "mcp:linear" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected mcp:linear in %v", m.sess.Allow)
	}
}

func TestTodoPaneIconsAndCounts(t *testing.T) {
	m := testModel(t)
	m.todos = []tools.TodoItem{
		{ID: "1", Content: "Explore", Status: tools.TodoInProgress},
		{ID: "2", Content: "Review", Status: tools.TodoPending},
		{ID: "3", Content: "Done", Status: tools.TodoCompleted},
		{ID: "4", Content: "Nope", Status: tools.TodoCancelled},
	}
	body := stripANSI(m.renderTodoPane(40))
	if !strings.Contains(body, "TODOS") || !strings.Contains(body, "1/3") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, "▶ Explore") || !strings.Contains(body, "□ Review") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, "✓ Done") || !strings.Contains(body, "✗ Nope") {
		t.Fatalf("%s", body)
	}
}

func TestMarketplaceLocalCatalogHint(t *testing.T) {
	m := testModel(t)
	m.openExtensions(2)
	if m.extTab != 2 {
		t.Fatal("marketplace tab")
	}
	found := false
	for _, it := range m.items {
		if strings.Contains(it.Label, "Local catalog") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", m.items)
	}
}
