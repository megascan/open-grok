package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGboomBareNeedsGraphicsToast(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM", "dumb")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("ITERM_SESSION_ID", "")
	m.handleSlash("/gboom")
	if lastSys(m) != gboomNoGraphicsToast {
		t.Fatalf("toast %q", lastSys(m))
	}
	if m.overlay == overlayGboom {
		t.Fatal("must not open overlay without graphics")
	}
}

func TestGboomOverlayChromeMatchesRust(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.openGboom()
	if m.overlay != overlayGboom {
		t.Fatal(m.overlay)
	}
	got := stripANSI(m.renderGboom())
	if !strings.Contains(got, "GBOOM") {
		t.Fatalf("title:\n%s", got)
	}
	if !strings.Contains(got, "HP 100") || !strings.Contains(got, "KILLS 0/8") {
		t.Fatalf("hud:\n%s", got)
	}
	if !strings.Contains(got, "ESC quit") {
		t.Fatalf("hint:\n%s", got)
	}
	m.handleGboomKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Fatal("esc should close")
	}
}
