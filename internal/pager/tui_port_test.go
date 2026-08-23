package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func testModel(t *testing.T) *model {
	t.Helper()
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	m, err := newModel(Options{
		CWD:   t.TempDir(),
		Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEscNeverClearsDashboardDraft(t *testing.T) {
	m := testModel(t)
	m.openDashboard()
	m.dashDraft = "fix the bug"
	m.dashListFocus = false
	m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dashDraft != "fix the bug" {
		t.Fatalf("first Esc cleared draft: %q", m.dashDraft)
	}
	if m.screen != screenDashboard {
		t.Fatal("first Esc must stay on dashboard")
	}
	if !m.dashListFocus {
		t.Fatal("first Esc blurs to the list")
	}
	m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dashDraft != "fix the bug" {
		t.Fatalf("second Esc cleared draft: %q", m.dashDraft)
	}
	if m.screen == screenDashboard {
		t.Fatal("second Esc leaves the dashboard")
	}
}

func TestPeekDigitsSelectWithoutSubmit(t *testing.T) {
	m := testModel(t)
	m.openDashboard()
	m.sess = &agent.Session{ID: "sess-1"}
	m.dashRows = []dashRow{{Sess: agent.Session{ID: "sess-1"}, State: "needs-input"}}
	m.cursor = 0
	m.peekQSel = -1
	m.question = &qAskMsg{
		Question: "Allow Edit?",
		Options:  []string{"allow", "deny"},
		Reply:    make(chan string, 1),
	}
	m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.peekQSel != 0 {
		t.Fatalf("1 must select option 0, got %d", m.peekQSel)
	}
	if len(m.question.Reply) != 0 {
		t.Fatal("1–9 must not submit until Enter")
	}
	m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.peekQSel != -1 {
		t.Fatalf("pressing 1 again toggles off, got %d", m.peekQSel)
	}
}

func TestToolVerbBashIsRanCommand(t *testing.T) {
	verb, noun := toolVerb("▸ bash")
	if verb != "Ran" || noun != "command" {
		t.Fatalf("got %s/%s", verb, noun)
	}
}

func TestWelcomeChooserKeysWhenSignedOut(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("GROK_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	m := testModel(t)
	if !m.unsignedIn() {
		t.Fatal("fresh ENTROPY_HOME must be unsigned")
	}
	if m.screen != screenWelcome {
		t.Fatalf("screen %d", m.screen)
	}
	found := false
	for _, it := range m.items {
		if it.Key == "codex" {
			found = true
		}
	}
	if !found {
		t.Fatalf("chooser items missing: %+v", m.items)
	}
	m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.screen != screenLogin {
		t.Fatalf("c must open login, screen=%d", m.screen)
	}
	if m.loginP != types.ProviderCodex {
		t.Fatalf("login provider %q", m.loginP)
	}

	m2 := testModel(t)
	m2.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m2.loginP != types.ProviderXAI {
		t.Fatalf("x must choose xAI, got %q", m2.loginP)
	}
}

func TestAcceptAtPathStillWritesAtPath(t *testing.T) {
	tok, _ := parseAtToken("read @fo")
	got := acceptAtPath("read @fo", tok, "foo.go", false, true)
	if got != "read @foo.go " {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(got, "@foo.go") {
		t.Fatal("chips are display-only; the buffer keeps @path")
	}
}

func TestInactiveCollapsedByDefault(t *testing.T) {
	m := testModel(t)
	if !m.inactiveCollapsed {
		t.Fatal("Inactive starts collapsed")
	}
}

func TestPageFlipAndGroupToolsDefaultOn(t *testing.T) {
	m := testModel(t)
	if !m.pageFlip || !m.groupTools || !m.simpleMode {
		t.Fatalf("pageFlip=%v groupTools=%v simpleMode=%v", m.pageFlip, m.groupTools, m.simpleMode)
	}
}

func TestRememberChipSkipsDirs(t *testing.T) {
	m := testModel(t)
	m.rememberChip("pkg/", true)
	m.rememberChip("main.go", false)
	m.rememberChip("main.go", false)
	if len(m.chips) != 1 || m.chips[0] != "main.go" {
		t.Fatalf("%v", m.chips)
	}
}

func TestSignedInWelcomeResumeWorktreeChangelog(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("GROK_API_KEY", "")
	m := testModel(t)
	if err := auth.SaveAPIKey(types.ProviderXAI, "xai-test-not-real", false); err != nil {
		t.Fatal(err)
	}
	m.refreshWelcome()
	if m.unsignedIn() {
		t.Fatal("expected signed in")
	}
	keys := map[string]bool{}
	for _, it := range m.items {
		keys[it.Key] = true
	}
	for _, want := range []string{"resume", "worktree", "changelog", "quit"} {
		if !keys[want] {
			t.Fatalf("missing %s in %+v", want, m.items)
		}
	}
	m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.overlay != overlayResume {
		t.Fatalf("ctrl+s must open resume picker, overlay=%d", m.overlay)
	}
}

func TestResumePickerListsAllCWDAndArmsDelete(t *testing.T) {
	m := testModel(t)
	other := t.TempDir()
	s1, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	s1.Title = "here"
	_ = s1.Persist()
	s2, err := agent.NewSession(other, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	s2.Title = "there"
	_ = s2.Persist()
	m.openResume()
	foundHere, foundThere, foundGroup := false, false, false
	for i, it := range m.items {
		if it.Key == "resume:"+s1.ID {
			foundHere = true
		}
		if it.Key == "resume:"+s2.ID {
			foundThere = true
			m.cursor = i
		}
		if strings.HasPrefix(it.Key, "group:") {
			foundGroup = true
		}
	}
	if !foundHere || !foundThere || !foundGroup {
		t.Fatalf("picker items %+v", m.items)
	}
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.overlay != overlayConfirm || m.confirmKind != "delete-welcome:"+s2.ID {
		t.Fatalf("d must arm delete, overlay=%d kind=%s", m.overlay, m.confirmKind)
	}
}

func TestWelcomePrintableStartsSession(t *testing.T) {
	m := testModel(t)
	if err := auth.SaveAPIKey(types.ProviderXAI, "xai-test-not-real", false); err != nil {
		t.Fatal(err)
	}
	m.refreshWelcome()
	m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.screen != screenChat {
		t.Fatalf("printable must start a session, screen=%d", m.screen)
	}
	if m.ta.Value() != "h" {
		t.Fatalf("prompt got %q", m.ta.Value())
	}
}

func TestFolderTrustYesPersists(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(Options{CWD: cwd, Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}})
	if err != nil {
		t.Fatal(err)
	}
	if !m.trustPending {
		t.Fatal("trust prompt required")
	}
	out := m.viewWelcome()
	if !strings.Contains(stripANSI(out), "Do you trust the contents of this directory") {
		t.Fatalf("trust copy missing: %s", stripANSI(out))
	}
	m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.trustPending {
		t.Fatal("y must grant")
	}
}

func TestImportClaudeToggleAndApply(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	entropy := t.TempDir()
	t.Setenv("ENTROPY_HOME", entropy)
	config.ResetHomeForTest()
	claude := filepath.Join(user, ".claude")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"permissions":{"allow":["Bash(*)"]}}`), 0o600)
	m, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}})
	if err != nil {
		t.Fatal(err)
	}
	if !m.hasClaudeImport() {
		t.Fatal("pending import")
	}
	m.openImportClaude()
	if m.overlay != overlayImportClaude {
		t.Fatal("overlay")
	}
	if len(m.importPlan.Items) == 0 {
		t.Fatal("scan")
	}
	m.handleImportClaudeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.importSel[0] {
		t.Fatal("space toggles off when starting selected")
	}
	m.handleImportClaudeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.handleImportClaudeKey(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, _ := config.Load()
	if !cfg.ClaudeCompat.Imported {
		t.Fatal("imported marker")
	}
	if len(cfg.Permission.Allow) == 0 {
		t.Fatal("allow rules")
	}
}
