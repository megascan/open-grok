package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
)

func TestSettingsEnumPickerOpensAndCommits(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	found := -1
	for i, it := range m.items {
		if it.Key == "screen_mode" {
			found = i
			break
		}
	}
	if found < 0 {
		t.Fatal("missing screen_mode")
	}
	m.cursor = found
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsEnumKey != "screen_mode" {
		t.Fatalf("picker key %q", m.settingsEnumKey)
	}
	body := stripANSI(m.renderSettings(72))
	if !strings.Contains(body, "fullscreen") || !strings.Contains(body, "minimal") {
		t.Fatalf("choices:\n%s", body)
	}
	if !strings.Contains(body, "Enter select") || !strings.Contains(body, "Esc cancel") {
		t.Fatalf("hints:\n%s", body)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsEnumKey != "" {
		t.Fatal("enter commits and closes picker")
	}
	if m.screenMd != modeMinimal {
		t.Fatalf("committed %q", m.screenMd)
	}
}

func TestSettingsEnumEscCancelsWithoutCommit(t *testing.T) {
	m := testModel(t)
	m.screenMd = modeFull
	m.openSettings()
	for i, it := range m.items {
		if it.Key == "code_mode" {
			m.cursor = i
			break
		}
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyDown})
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.settingsEnumKey != "" {
		t.Fatal("esc returns to browse")
	}
	if m.overlay != overlaySettings {
		t.Fatal("esc in picker must not close settings")
	}
	if m.codeMode != "direct" {
		t.Fatalf("esc must not commit, got %q", m.codeMode)
	}
}

func TestSettingsSecretEditorMasksAndSaves(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	for i, it := range m.items {
		if it.Key == "fireworks_api_key" {
			m.cursor = i
			break
		}
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settingsSecretKey != "fireworks_api_key" {
		t.Fatalf("secret key %q", m.settingsSecretKey)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fw-secret")})
	body := stripANSI(m.renderSettings(72))
	if strings.Contains(body, "fw-secret") {
		t.Fatal("secret must be masked")
	}
	if !strings.Contains(body, "Enter commit") {
		t.Fatalf("hints:\n%s", body)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := auth.Lookup(types.ProviderFireworks, false); !ok {
		t.Fatal("key should be saved")
	}
}

func TestPermissionChromeTitleAndFollowup(t *testing.T) {
	m := testModel(t)
	ch := make(chan bool, 1)
	m.perm = &permAskMsg{Tool: "search_replace", Access: "edit", Path: "/tmp/x.go", Reply: ch}
	m.initPermCard()
	if m.permTitle() != "Allow Edit to /tmp/x.go?" {
		t.Fatalf("title %q", m.permTitle())
	}
	view := stripANSI(m.renderPermission(72))
	if !strings.Contains(view, "Allow Edit to /tmp/x.go?") {
		t.Fatalf("missing title:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("missing accent bar")
	}
	if !strings.Contains(view, permFollowupLabel) {
		t.Fatalf("followup:\n%s", view)
	}
	if strings.Contains(view, "Tell Entropy") {
		t.Fatal("old followup copy")
	}

	m.perm = &permAskMsg{Tool: "bash", Access: "bash", Command: "cargo test --all", Reply: ch}
	m.initPermCard()
	if m.permTitle() != "Allow `cargo`?" {
		t.Fatalf("bash title %q", m.permTitle())
	}
	view = stripANSI(m.renderPermission(72))
	if !strings.Contains(view, "narrow scope") || !strings.Contains(view, "e edit pattern") {
		t.Fatalf("scope hint:\n%s", view)
	}
}

func TestPermissionFollowupLabelIsRejectOnce(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.perm = &permAskMsg{Tool: "edit", Access: "edit", Reply: make(chan bool, 1)}
	m.initPermCard()
	choices := m.permChoices()
	last := choices[len(choices)-1]
	if last.ID != "followup" || last.Label != permFollowupLabel {
		t.Fatalf("%+v", last)
	}
}
