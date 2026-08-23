package pager

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/gboom"
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
	hud := m.gboom.HUD()
	wantKills := fmt.Sprintf("KILLS %d/%d", hud.Kills, hud.Total)
	if !strings.Contains(got, "HP 100") || !strings.Contains(got, wantKills) {
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

func TestGboomPlayableLoopStartsAndRendersFrame(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.openGboom()
	if m.gboom.Phase() != gboom.PhaseTitle {
		t.Fatal("must start on title")
	}
	view := m.renderGboom()
	if !strings.Contains(view, "▀") {
		t.Fatalf("title frame must paint half-blocks:\n%s", stripANSI(view))
	}
	m.handleGboomKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.gboom.Phase() != gboom.PhasePlaying {
		t.Fatal(m.gboom.Phase())
	}
	got := stripANSI(m.renderGboom())
	if !strings.Contains(got, "SPACE fire") {
		t.Fatalf("playing hint:\n%s", got)
	}
	m.handleGboomTick()
	m.handleGboomKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m.gboom.Tick()
	if m.gboom.Game().Player.Muzzle <= 0 {
		t.Fatal("space must queue a shot that fires on the next tick")
	}
}

func TestGboomSlashOpensPlayableWhenGraphicsDetected(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	cmd := m.handleSlash("/gboom")
	if m.overlay != overlayGboom || m.gboom == nil {
		t.Fatal("graphics terminal must open the game")
	}
	if cmd == nil {
		t.Fatal("must schedule the 30fps tick")
	}
}
