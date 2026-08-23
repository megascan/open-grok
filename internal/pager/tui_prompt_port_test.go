package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

func TestPromptPrefixIsGrokArrow(t *testing.T) {
	m := testModel(t)
	if m.ta.Prompt != promptArrow {
		t.Fatalf("prompt %q want %q", m.ta.Prompt, promptArrow)
	}
	m.width, m.height = 80, 24
	ghost := m.renderPromptInput()
	if !strings.Contains(stripANSI(ghost), "\u276F") && !strings.Contains(m.ta.Prompt, "\u276F") {
		t.Fatalf("expected ❯ prompt, got %q", m.ta.Prompt)
	}
}

func TestFileDropdownIsInlineNotCenteredModal(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	dir := t.TempDir()
	m.opts.CWD = dir
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.ta.SetValue("see @al")
	m.refreshFileSearch()
	if m.overlay != overlayFiles {
		t.Fatal("expected file overlay")
	}
	dd := m.renderFileDropdown()
	plain := stripANSI(dd)
	if !strings.Contains(plain, "alpha.go") {
		t.Fatalf("missing file row:\n%s", plain)
	}
	if strings.Count(plain, "\n")+1 > maxFileDropdownRows {
		t.Fatalf("file dropdown taller than %d:\n%s", maxFileDropdownRows, plain)
	}
	if m.viewWithOverlay("BASE") != "BASE" {
		t.Fatal("@ files dropdown must not paint a centered overlay")
	}
}

func TestSlashArgSuggestionsForModelAndTheme(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.overlay = overlayPalette
	m.ta.SetValue("/theme ")
	m.filter = "/theme "
	m.cursor = 0
	rows := m.slashDropdownRows()
	if len(rows) < 2 || rows[0].Label != "auto" {
		t.Fatalf("theme args: %+v", rows)
	}
	found := false
	for _, r := range rows {
		if r.Label == "groknight" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing groknight: %+v", rows)
	}
	dd := m.renderSlashDropdown()
	if m.viewWithOverlay("BASE") != "BASE" {
		t.Fatal("arg dropdown must stay inline")
	}
	if !strings.Contains(stripANSI(dd), "auto") {
		t.Fatalf("theme dropdown:\n%s", stripANSI(dd))
	}

	m.ta.SetValue("/announcements ")
	rows = m.slashDropdownRows()
	if len(rows) != 2 || rows[0].Label != "hide" || rows[1].Label != "show" {
		t.Fatalf("announcements args %+v", rows)
	}

	m.ta.SetValue("/model ")
	rows = m.slashDropdownRows()
	if len(rows) == 0 {
		t.Fatal("model args should list catalog")
	}
}

func TestThemeEmptyTogglesAndUnknownErrors(t *testing.T) {
	m := testModel(t)
	m.theme = "groknight"
	applyTheme(resolveTheme(m.theme))
	m.handleSlash("/theme")
	if canonicalThemeName(m.theme) != "grokday" {
		t.Fatalf("toggle got %q", m.theme)
	}
	if lastSys(m) != "\u2713 Theme: Grok Day" {
		t.Fatalf("toast %q", lastSys(m))
	}
	m.handleSlash("/theme not-a-theme")
	if lastSys(m) != unknownThemeError("not-a-theme") {
		t.Fatalf("unknown %q", lastSys(m))
	}
	m.handleSlash("/theme auto")
	if m.theme != "auto" {
		t.Fatalf("auto %q", m.theme)
	}
}

func TestPrivacyOpensSettingsFocusIgnoringArgs(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/privacy opt-out")
	if m.overlay != overlaySettings {
		t.Fatalf("overlay %d", m.overlay)
	}
	if m.cursor >= len(m.items) || m.items[m.cursor].Key != "coding_data_sharing" {
		t.Fatalf("focus %+v cursor=%d", m.items[min(m.cursor, len(m.items)-1)], m.cursor)
	}
}

func TestAnnouncementsHideShowAndUsage(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	if !m.showAnnouncementBanner() {
		t.Fatal("banner should show with a session")
	}
	m.handleSlash("/announcements")
	if lastSys(m) != "Usage: /announcements hide | show" {
		t.Fatalf("usage %q", lastSys(m))
	}
	m.handleSlash("/announcements hide")
	if m.showAnnouncementBanner() {
		t.Fatal("hide should dismiss banner")
	}
	m.handleSlash("/announcements show")
	if !m.showAnnouncementBanner() {
		t.Fatal("show should restore banner")
	}
}

func TestAlwaysApproveToastsDoNotExitPlan(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.sess.Plan.Enter(m.sess.Plan.PlanFile)
	m.handleSlash("/always-approve")
	if m.permMode != permAlways || !m.sess.YOLO {
		t.Fatal("should enable yolo")
	}
	if !m.sess.Plan.Active {
		t.Fatal("plan gate must stay armed")
	}
	if lastSys(m) != "\u26A0 Always-approve ON: plan mode still blocks file edits until you exit plan mode" {
		t.Fatalf("plan toast %q", lastSys(m))
	}
	m.sess.Plan.Exit()
	m.permMode = permAsk
	m.handleSlash("/always-approve")
	if lastSys(m) != "\u26A0 Always-approve ON: all tool actions auto-run" {
		t.Fatalf("on toast %q", lastSys(m))
	}
	m.handleSlash("/always-approve")
	if lastSys(m) != "\u2713 Always-approve: off" {
		t.Fatalf("off toast %q", lastSys(m))
	}
}

func TestToggleToastsMatchRustSaveSuccess(t *testing.T) {
	m := testModel(t)
	startML := m.multiline
	m.handleSlash("/multiline")
	if lastSys(m) != saveSuccessToast("Multiline", !startML) {
		t.Fatalf("multiline %q", lastSys(m))
	}
	startC := m.compactUI
	m.handleSlash("/compact-mode")
	if lastSys(m) != saveSuccessToast("Compact mode", !startC) {
		t.Fatalf("compact %q", lastSys(m))
	}
	startV := m.vimMode
	m.handleSlash("/vim-mode")
	if lastSys(m) != saveSuccessToast("Vim scrollback", !startV) {
		t.Fatalf("vim %q", lastSys(m))
	}
	startT := m.timestamps
	m.handleSlash("/timestamps")
	if lastSys(m) != saveSuccessToast("Timestamps", !startT) {
		t.Fatalf("timestamps %q", lastSys(m))
	}
}

func TestScreenModeSessionAndAlreadyInMode(t *testing.T) {
	m := testModel(t)
	m.screenMd = modeFull
	m.handleSlash("/minimal")
	if lastSys(m) != "No active session to reopen in minimal mode" {
		t.Fatalf("minimal no sess %q", lastSys(m))
	}
	m.handleSlash("/fullscreen")
	if lastSys(m) != "You're already in fullscreen mode." {
		t.Fatalf("already full %q", lastSys(m))
	}
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/minimal")
	if m.screenMd != modeMinimal {
		t.Fatal("should switch to minimal")
	}
	m.handleSlash("/minimal")
	if lastSys(m) != "You're already in minimal mode." {
		t.Fatalf("already min %q", lastSys(m))
	}
}

func TestFastRequiresSessionAndCatalogSupport(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/fast")
	if lastSys(m) != "No active session" {
		t.Fatalf("no sess %q", lastSys(m))
	}
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/fast")
	if lastSys(m) != "current model does not support Fast mode" {
		t.Fatalf("no fast %q", lastSys(m))
	}
	m.opts.Model = catalog.Model{ID: "gpt-5.4", Name: "GPT", Provider: types.ProviderCodex}
	m.handleSlash("/fast")
	if !m.fastTier || lastSys(m) != saveSuccessToast("Fast mode", true) {
		t.Fatalf("enable %v %q", m.fastTier, lastSys(m))
	}
}

func TestSessionScopedLeftoversAndGboomPassThrough(t *testing.T) {
	m := testModel(t)
	for _, cmd := range []string{"/history", "/queue", "/plan", "/view-plan", "/btw hi", "/gboom"} {
		m.handleSlash(cmd)
		if lastSys(m) != "No active session" && m.status != "No active session" {
			t.Fatalf("%s: %q status %q", cmd, lastSys(m), m.status)
		}
	}
	m.screenMd = modeMinimal
	m.handleSlash("/expand")
	if lastSys(m) != "No active session" {
		t.Fatalf("expand: %q", lastSys(m))
	}
	m.handleSlash("/edit-prompt")
	if lastSys(m) != "No active session" {
		t.Fatalf("edit-prompt: %q", lastSys(m))
	}
	m.handleSlash("/btw")
	if lastSys(m) != "No active session" {
		t.Fatalf("btw no sess %q", lastSys(m))
	}
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/btw")
	if lastSys(m) != "Usage: /btw <question>" {
		t.Fatalf("btw usage %q", lastSys(m))
	}
	m.handleSlash("/model")
	if lastSys(m) != "Usage: /model <name> [effort]" {
		t.Fatalf("model usage %q", lastSys(m))
	}
}

func TestHistorySearchIsInlineWithQueryPrefix(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.history = []string{"first prompt", "second prompt"}
	m.handleSlash("/history")
	if m.overlay != overlayHistory {
		t.Fatal("expected history overlay")
	}
	if m.ta.Prompt != historySearchPrefix {
		t.Fatalf("prefix %q", m.ta.Prompt)
	}
	if m.viewWithOverlay("BASE") != "BASE" {
		t.Fatal("history must not paint a centered overlay")
	}
	m.ta.SetValue("second")
	m.refreshHistorySearch()
	dd := stripANSI(m.renderHistoryDropdown())
	if !strings.Contains(dd, "second") {
		t.Fatalf("filter:\n%s", dd)
	}
	m.handleHistoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ta.Value() != "second prompt" {
		t.Fatalf("insert %q", m.ta.Value())
	}
	if m.overlay != overlayNone {
		t.Fatal("enter closes history")
	}
}
