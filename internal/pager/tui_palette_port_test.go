package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func TestSlashDropdownIsInlineNotCenteredModal(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.screen = screenChat
	m.overlay = overlayPalette
	m.ta.SetValue("/he")
	m.filter = "/he"
	m.cursor = 0
	dd := m.renderSlashDropdown()
	plain := stripANSI(dd)
	if !strings.Contains(plain, "\u276F") {
		t.Fatalf("selected row needs ❯ prefix:\n%s", plain)
	}
	if !strings.Contains(plain, "/help") {
		t.Fatalf("expected /help row:\n%s", plain)
	}
	if strings.Count(plain, "\n")+1 > maxVisibleSuggestions {
		t.Fatalf("dropdown taller than %d rows:\n%s", maxVisibleSuggestions, plain)
	}
	if m.viewWithOverlay("BASE") != "BASE" {
		t.Fatal("slash dropdown must not paint a centered overlay")
	}
}

func TestCommandPaletteGroupsAndFilter(t *testing.T) {
	m := testModel(t)
	m.screen = screenChat
	m.openHelp()
	if m.overlay != overlayHelp {
		t.Fatal("expected command palette")
	}
	if m.overlayTitle() != "Commands" {
		t.Fatalf("title %q", m.overlayTitle())
	}
	if len(m.items) < 8 || !m.items[0].Header || m.items[0].Label != "Session" {
		t.Fatalf("expected Session header, got %+v", m.items[:min(3, len(m.items))])
	}
	var labels []string
	for _, it := range m.items {
		if !it.Header {
			labels = append(labels, it.Label)
		}
	}
	for _, want := range []string{"New Session", "New Session in Worktree", "Settings", "Keyboard Shortcuts", "Quit"} {
		found := false
		for _, l := range labels {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing palette row %q in %v", want, labels)
		}
	}
	if m.items[m.cursor].Header {
		t.Fatal("cursor should skip section headers")
	}
	filtered := filterPaletteEntries("theme", m.items)
	if len(filtered) < 2 || !filtered[0].Header || filtered[0].Label != "Other" {
		t.Fatalf("theme filter should keep Other header: %+v", filtered)
	}
	if filtered[1].Label != "Switch Theme" {
		t.Fatalf("got %+v", filtered)
	}
	m.toggleCommandPalette()
	if m.overlay != overlayNone {
		t.Fatal("second /help toggles closed")
	}
}

func TestCommandPaletteMinimalDropsFullscreenRows(t *testing.T) {
	entries := defaultPaletteEntries(modeMinimal)
	for _, it := range entries {
		if it.Key == "slash:/dashboard" || it.Label == "Agent Dashboard" {
			t.Fatal("dashboard is fullscreen-only")
		}
		if it.Key == "edit-prompt" {
			return
		}
	}
	t.Fatal("minimal palette must include Edit Prompt in External Editor")
}

func TestNewSessionWorktreeAskNeverAndWelcomeSkip(t *testing.T) {
	m := testModel(t)
	gitInit(t, m.opts.CWD)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.screen = screenChat
	m.handleSlash("/new")
	if m.overlay == overlayQuestion {
		t.Fatal("default never must not open worktree question")
	}

	m.cfg.Prefs = config.Prefs{"new_session_worktree_mode": "ask"}
	m.handleSlash("/new")
	if m.overlay != overlayQuestion || m.question == nil {
		t.Fatal("ask mode should open worktree question")
	}
	if m.question.Question != "Start the new session in an isolated git worktree?" {
		t.Fatalf("%q", m.question.Question)
	}
	if got := strings.Join(m.question.Options, "|"); got != "Yes|No|Always worktree|Never worktree" {
		t.Fatalf("options %s", got)
	}
	view := stripANSI(m.renderQuestion(72))
	if strings.Contains(view, "Other") || strings.Contains(view, questionFreeformPlaceholder) {
		t.Fatalf("local new-session question must not show Other:\n%s", view)
	}

	m.overlay = overlayNone
	m.question = nil
	m.screen = screenWelcome
	m.handleSlash("/new")
	if m.overlay == overlayQuestion {
		t.Fatal("welcome /new must not open the worktree modal")
	}
}

func TestExportTranscriptRenameDeleteFindAddDirEffort(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/export")
	if m.status != "No active session to export" {
		t.Fatalf("export: %q", m.status)
	}
	m.handleSlash("/transcript")
	if m.status != "No active session to view" {
		t.Fatalf("transcript: %q", m.status)
	}
	m.handleSlash("/rename hello")
	if m.status != "No active session" {
		t.Fatalf("rename: %q", m.status)
	}
	m.handleSlash("/delete")
	if m.status != "No active session to delete" || m.overlay == overlayConfirm {
		t.Fatalf("delete: %q overlay=%v", m.status, m.overlay)
	}
	m.handleSlash("/find")
	if m.status != "No active session" {
		t.Fatalf("find: %q", m.status)
	}
	m.handleSlash("/add-dir")
	if m.status != "No active session" {
		t.Fatalf("add-dir: %q", m.status)
	}
	m.handleSlash("/effort")
	if m.status != "No active session" {
		t.Fatalf("effort: %q", m.status)
	}

	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/export")
	if lastSys(m) != "No conversation content to export" {
		t.Fatalf("empty export: %q", lastSys(m))
	}
	m.handleSlash("/transcript")
	if lastSys(m) != "No conversation transcript to view yet" {
		t.Fatalf("empty transcript: %q", lastSys(m))
	}

	m.sess.Messages = []types.Message{{Role: types.RoleUser, Content: "hi"}, {Role: types.RoleAssistant, Content: "hello"}}
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "chat.md")
	m.handleSlash("/export " + out)
	if lastSys(m) != "Conversation exported to "+out {
		t.Fatalf("file export: %q", lastSys(m))
	}
	got, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(got), "hello") {
		t.Fatalf("wrote %q %v", got, err)
	}

	m.handleSlash("/rename")
	if lastSys(m) != "Usage: /rename <new title> | --auto" {
		t.Fatalf("rename usage: %q", lastSys(m))
	}
	m.handleSlash("/rename --auto extra")
	if lastSys(m) != "--auto takes no title" {
		t.Fatalf("auto trailing: %q", lastSys(m))
	}
	m.handleSlash("/rename --auto")
	if m.sess.Title != "" || lastSys(m) != "title reset to auto" {
		t.Fatalf("auto: title=%q %q", m.sess.Title, lastSys(m))
	}
	m.handleSlash("/rename  hello  ")
	if m.sess.Title != "hello" {
		t.Fatalf("title %q", m.sess.Title)
	}

	m.handleSlash("/add-dir")
	if lastSys(m) != "Usage: /add-dir <path>" {
		t.Fatalf("add-dir usage: %q", lastSys(m))
	}
	m.handleSlash("/add-dir /definitely-not-a-dir-entropy")
	if lastSys(m) != "Not a directory: /definitely-not-a-dir-entropy" {
		t.Fatalf("not dir: %q", lastSys(m))
	}

	m.handleSlash("/effort")
	if lastSys(m) != "Usage: /effort <xhigh|high|medium|low>" {
		t.Fatalf("effort usage: %q", lastSys(m))
	}
	m.handleSlash("/effort turbo")
	if lastSys(m) != "unknown effort level 'turbo'; use one of: xhigh, high, medium, low" {
		t.Fatalf("effort unknown: %q", lastSys(m))
	}
	m.handleSlash("/effort high")
	if m.sess.Effort != "high" {
		t.Fatalf("effort %q", m.sess.Effort)
	}
	m.handleSlash("/effort")
	if lastSys(m) != "Usage: /effort <xhigh|high|medium|low> (current: high)" {
		t.Fatalf("effort current: %q", lastSys(m))
	}
}

func TestSanitizeRenameTitleMatchesRust(t *testing.T) {
	if got := sanitizeRenameTitle("  hello  "); got != "hello" {
		t.Fatalf("%q", got)
	}
	if got := sanitizeRenameTitle("\u001bhi"); got != "hi" {
		t.Fatalf("controls: %q", got)
	}
	too := strings.Repeat("é", maxTitleScalars+1)
	_, _, err := parseRenameArg(too)
	if err != "title too long (max 100 characters)" {
		t.Fatalf("%q", err)
	}
	ok := strings.Repeat("é", maxTitleScalars)
	title, auto, err := parseRenameArg(ok)
	if err != "" || auto || utf8.RuneCountInString(title) != maxTitleScalars {
		t.Fatalf("%q %v %q", title, auto, err)
	}
}

func TestFindLivePreviewRestoresOnEsc(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.width, m.height = 80, 24
	m.layoutChat()
	m.blocks = []block{
		{kind: blockUser, body: "alpha turn"},
		{kind: blockAgent, body: "reply one"},
		{kind: blockUser, body: "beta turn"},
		{kind: blockAgent, body: "reply two"},
	}
	m.refreshVP()
	m.vp.GotoBottom()
	start := m.vp.YOffset
	m.openFind("alpha")
	if m.overlay != overlayFind {
		t.Fatal("expected find overlay")
	}
	if m.vp.YOffset == start && start != 0 {
		t.Fatal("find should live-scroll to the match")
	}
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Fatal("esc closes find")
	}
	if m.vp.YOffset != start {
		t.Fatalf("esc should restore viewport %d got %d", start, m.vp.YOffset)
	}
}

func lastSys(m *model) string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		switch m.blocks[i].kind {
		case blockSystem:
			return m.blocks[i].body
		case blockSession:
			return m.blocks[i].event.message()
		}
	}
	return m.status
}
