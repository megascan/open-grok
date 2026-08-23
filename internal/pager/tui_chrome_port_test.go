package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
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

func TestQuestionChromeAccentAndFreeformPlaceholder(t *testing.T) {
	m := testModel(t)
	m.question = &qAskMsg{
		Question: "Which database?\n\nPick the engine for the backend.",
		Options:  []string{"postgres", "sqlite"},
		Reply:    make(chan string, 1),
	}
	view := stripANSI(m.renderQuestion(72))
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent bar")
	}
	if !strings.Contains(view, "Which database?") || !strings.Contains(view, "Pick the engine for the backend.") {
		t.Fatalf("label/desc:\n%s", view)
	}
	if strings.Contains(view, "Question 1/") || strings.HasPrefix(strings.TrimSpace(view), "Question") {
		t.Fatalf("no Question title:\n%s", view)
	}
	if !strings.Contains(view, questionFreeformPlaceholder) {
		t.Fatalf("freeform:\n%s", view)
	}
	m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	view = stripANSI(m.renderQuestion(72))
	if !strings.Contains(view, "("+pickerDotFilled+")") {
		t.Fatalf("space selects:\n%s", view)
	}
}

func TestQuestionEnterAdvancesThenSubmits(t *testing.T) {
	m := testModel(t)
	ch := make(chan string, 1)
	m.question = &qAskMsg{
		Questions: []questionItem{
			{Question: "Color?", Options: []string{"red", "blue"}},
			{Question: "Size?", Options: []string{"s", "m"}},
		},
		Reply: ch,
	}
	m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.question == nil || m.question.Index != 1 {
		t.Fatal("enter on first question advances")
	}
	if len(ch) != 0 {
		t.Fatal("must not submit until last question")
	}
	m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.question != nil {
		t.Fatal("enter on last submits")
	}
	got := <-ch
	if got != "red\ns" {
		t.Fatalf("answers %q", got)
	}
}

func TestPermissionQueueHoldsLaterRequests(t *testing.T) {
	m := testModel(t)
	first := make(chan bool, 1)
	second := make(chan bool, 1)
	m.showPerm(permAskMsg{Tool: "bash", Access: "bash", Command: "ls", Reply: first})
	_, _ = m.Update(permAskMsg{Tool: "search_replace", Access: "edit", Path: "/tmp/x.go", Reply: second})
	if m.perm == nil || m.perm.Tool != "bash" {
		t.Fatalf("front %v", m.perm)
	}
	if len(m.permQueue) != 1 || m.permQueue[0].Tool != "search_replace" {
		t.Fatalf("queue %+v", m.permQueue)
	}
	m.replyPerm(true, false, false)
	if len(first) != 1 {
		t.Fatal("first reply delivered")
	}
	if m.perm == nil || m.perm.Tool != "search_replace" {
		t.Fatalf("rotated to %v", m.perm)
	}
	if m.permTitle() != "Allow Edit to /tmp/x.go?" {
		t.Fatalf("title %q", m.permTitle())
	}
}

func TestWelcomeTrustCopyMatchesRust(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.trustPending = true
	m.refreshWelcome()
	got := stripANSI(m.viewWelcome())
	if !strings.Contains(got, welcomeTrustQuestion) {
		t.Fatalf("question:\n%s", got)
	}
	if !strings.Contains(got, welcomeTrustWarningLine1) || !strings.Contains(got, welcomeTrustWarningLine2) {
		t.Fatalf("warning:\n%s", got)
	}
	if strings.Contains(got, "Entropy may run") {
		t.Fatal("trust warning must use Open Grok copy")
	}
	if !strings.Contains(got, "Yes, proceed") || !strings.Contains(got, "No, quit") {
		t.Fatalf("menu:\n%s", got)
	}
}

func TestDashboardPeekPermissionAndQuestionChrome(t *testing.T) {
	m := testModel(t)
	m.openDashboard()
	m.sess = &agent.Session{ID: "sess-1"}
	m.dashRows = []dashRow{{Sess: agent.Session{ID: "sess-1"}, State: "needs-input"}}
	m.cursor = 0
	m.perm = &permAskMsg{Tool: "search_replace", Access: "edit", Path: "/tmp/x.go", Reply: make(chan bool, 1)}
	m.initPermCard()
	view := stripANSI(m.renderDashPeekPanel())
	if !strings.Contains(view, "Allow Edit to /tmp/x.go?") {
		t.Fatalf("perm title:\n%s", view)
	}
	if !strings.Contains(view, permFollowupLabel) {
		t.Fatalf("reject:\n%s", view)
	}
	if !strings.Contains(view, "1. ") {
		t.Fatalf("numbered:\n%s", view)
	}

	m.perm = nil
	m.question = &qAskMsg{
		Questions: []questionItem{
			{Question: "Color?", Options: []string{"red", "blue"}},
			{Question: "Size?", Options: []string{"s", "m"}},
		},
		Reply: make(chan string, 1),
	}
	view = stripANSI(m.renderDashPeekPanel())
	if !strings.Contains(view, "(1/2) Color?") {
		t.Fatalf("multi-question marker:\n%s", view)
	}
	if !strings.Contains(view, peekOtherPlaceholder) {
		t.Fatalf("other:\n%s", view)
	}
}

func TestFeedbackPaneQuestionChromeAndPlaceholder(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.width = 80
	m.handleSlash("/feedback")
	view := stripANSI(m.renderOverlay())
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent")
	}
	if !strings.Contains(view, feedbackQuestionLabel) {
		t.Fatalf("label:\n%s", view)
	}
	if !strings.Contains(view, "Please provide as much") || !strings.Contains(view, "detail as possible") {
		t.Fatalf("placeholder:\n%s", view)
	}
	m.question = &qAskMsg{Question: "busy?", Options: []string{"yes"}}
	m.overlay = overlayNone
	m.handleSlash("/feedback")
	if lastSys(m) != feedbackBlockedQuestion {
		t.Fatalf("blocked %q", lastSys(m))
	}
}

func TestPlanApprovalWaitingChrome(t *testing.T) {
	m := testModel(t)
	m.plan = &planAskMsg{HasPlan: true, Plan: "# Ship it\n\nDo the thing."}
	view := stripANSI(m.renderPlanApproval(72))
	if !strings.Contains(view, planWaitingLabel) {
		t.Fatalf("waiting:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent")
	}
	m.plan.HasPlan = false
	m.plan.Plan = ""
	view = stripANSI(m.renderPlanApproval(72))
	if !strings.Contains(view, planEmptyTitle) {
		t.Fatalf("empty:\n%s", view)
	}
}

func TestThemePickerAutoFirstAndEscReverts(t *testing.T) {
	m := testModel(t)
	m.theme = "groknight"
	applyTheme(resolveTheme(m.theme))
	m.openTheme()
	if m.items[0].Key != "auto" {
		t.Fatalf("auto first %+v", m.items[0])
	}
	if m.items[1].Key != "groknight" || !strings.Contains(m.items[1].Detail, "(active)") {
		t.Fatalf("active %+v", m.items[1])
	}
	view := stripANSI(m.renderThemePicker(72))
	if !strings.Contains(view, permAccentBar) || !strings.Contains(view, "("+pickerDotFilled+")") {
		t.Fatalf("radio:\n%s", view)
	}
	m.cursor = 2
	m.previewOverlay()
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEsc})
	if canonicalThemeName(m.theme) != "groknight" {
		t.Fatalf("esc revert %q", m.theme)
	}
	if m.overlay != overlayNone {
		t.Fatal("esc closes")
	}
}

func TestLoginPickerRadioChrome(t *testing.T) {
	m := testModel(t)
	m.openLogin("")
	view := stripANSI(m.loginBody())
	if !strings.Contains(view, "Choose a provider") {
		t.Fatal("title")
	}
	if !strings.Contains(view, permAccentBar) || !strings.Contains(view, "("+pickerDotFilled+")") {
		t.Fatalf("radio:\n%s", view)
	}
	if !strings.Contains(view, "xAI Grok") || !strings.Contains(view, "ChatGPT Codex") {
		t.Fatalf("providers:\n%s", view)
	}
}

func TestModelPickerProviderCurrentAndContext(t *testing.T) {
	m := testModel(t)
	mods := catalog.All()
	if len(mods) == 0 {
		t.Fatal("catalog empty")
	}
	m.opts.Model = mods[0]
	m.openModels()
	if len(m.items) == 0 {
		t.Fatal("picker empty")
	}
	foundCurrent := false
	for _, it := range m.items {
		if strings.Contains(it.Label, "(current)") {
			foundCurrent = true
		}
		if it.Key == m.opts.Model.ID && !strings.Contains(it.Label, " · ") {
			t.Fatalf("provider · name missing: %q", it.Label)
		}
	}
	if !foundCurrent {
		t.Fatalf("current missing %+v", m.items[0])
	}
	view := stripANSI(m.renderModelPicker(72))
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent")
	}
}

func TestCancelTurnAccentBar(t *testing.T) {
	m := testModel(t)
	m.cancel = &cancelTurnState{Running: 1}
	view := stripANSI(m.renderCancelTurn(60))
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent")
	}
	if !strings.Contains(view, "1 subagent running") {
		t.Fatalf("%s", view)
	}
}
