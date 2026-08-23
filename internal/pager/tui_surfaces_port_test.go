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
