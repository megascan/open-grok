package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
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
