package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIntStepSizesMatchRustRangePolicy(t *testing.T) {
	cases := []struct {
		min, max, small, large int64
	}{
		{1, 10, 1, 1},    // scroll_lines span 9
		{1, 100, 1, 5},   // scroll_speed span 99
		{40, 500, 5, 10}, // max_thoughts_width span 460
		{0, 0, 1, 1},
		{1, 21, 1, 4},  // span 20 still narrow: large = span/5
		{1, 22, 1, 5},  // span 21 → mid band
		{1, 101, 1, 5}, // span 100 still mid
		{1, 102, 5, 10},
	}
	for _, c := range cases {
		small, large := intStepSizes(c.min, c.max)
		if small != c.small || large != c.large {
			t.Fatalf("intStepSizes(%d,%d)=(%d,%d) want (%d,%d)",
				c.min, c.max, small, large, c.small, c.large)
		}
	}
}

func TestSettingsIntEditorStepsAndCommits(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	found := false
	for i, it := range m.items {
		if it.Key == "scroll_speed" {
			m.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("scroll_speed missing from catalog")
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsEditKey != "scroll_speed" || m.settingsEditKind != "int" {
		t.Fatalf("editor key=%q kind=%q", m.settingsEditKey, m.settingsEditKind)
	}
	if m.settingsEditBuf != "50" {
		t.Fatalf("default buffer %q", m.settingsEditBuf)
	}
	body := stripANSI(m.renderSettings(72))
	if !strings.Contains(body, "Settings \u203a Scroll speed") {
		t.Fatalf("breadcrumb:\n%s", body)
	}
	if !strings.Contains(body, "\u2039") || !strings.Contains(body, "50") {
		t.Fatalf("stepper:\n%s", body)
	}
	if !strings.Contains(body, "Enter save") {
		t.Fatalf("footer:\n%s", body)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.settingsEditBuf != "51" {
		t.Fatalf("up small step %q", m.settingsEditBuf)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRight})
	if m.settingsEditBuf != "56" {
		t.Fatalf("right large step %q", m.settingsEditBuf)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsEditKey != "" {
		t.Fatal("enter should commit and close editor")
	}
	if m.cfg.Prefs["scroll_speed"] != "56" {
		t.Fatalf("prefs %q", m.cfg.Prefs["scroll_speed"])
	}
}

func TestSettingsIntEditorEscCancels(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	for i, it := range m.items {
		if it.Key == "scroll_lines" {
			m.cursor = i
			break
		}
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyUp})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.settingsEditKey != "" {
		t.Fatal("esc should cancel")
	}
	if m.cfg.Prefs["scroll_lines"] != "" {
		t.Fatalf("esc must not persist, got %q", m.cfg.Prefs["scroll_lines"])
	}
	if m.overlay != overlaySettings {
		t.Fatal("esc from editor returns to browse, not close")
	}
}

func TestSettingsStringEditorTypesAndSaves(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	found := false
	for i, it := range m.items {
		if it.Key == "custom_model_id" {
			m.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("custom_model_id missing")
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsEditKind != "string" {
		t.Fatalf("kind %q", m.settingsEditKind)
	}
	body := stripANSI(m.renderSettings(72))
	if !strings.Contains(body, stringPlaceholder) {
		t.Fatalf("placeholder:\n%s", body)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zai:glm")})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.Prefs["custom_model_id"] != "zai:glm" {
		t.Fatalf("saved %q", m.cfg.Prefs["custom_model_id"])
	}
}

func TestSettingsIntClampsToBounds(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	for i, it := range m.items {
		if it.Key == "scroll_lines" {
			m.cursor = i
			break
		}
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 20; i++ {
		m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.settingsEditBuf != "1" {
		t.Fatalf("clamped min %q", m.settingsEditBuf)
	}
	for i := 0; i < 20; i++ {
		m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.settingsEditBuf != "10" {
		t.Fatalf("clamped max %q", m.settingsEditBuf)
	}
}
