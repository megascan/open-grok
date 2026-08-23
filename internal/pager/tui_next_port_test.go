package pager

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/diagnostics"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestDoctorReportOverlayMatchesRustLayout(t *testing.T) {
	m := testModel(t)
	m.handleDoctor("")
	if m.overlay != overlayDoctor {
		t.Fatalf("overlay %d", m.overlay)
	}
	if !strings.Contains(m.doctorReport, "Environment\n") || !strings.Contains(m.doctorReport, "Clipboard\n") || !strings.Contains(m.doctorReport, "Voice\n") {
		t.Fatalf("report:\n%s", m.doctorReport)
	}
	view := m.renderDoctor(72)
	if !strings.Contains(view, "Doctor") {
		t.Fatalf("view %s", view)
	}
}

func TestDoctorUnknownArgsPrintUsage(t *testing.T) {
	m := testModel(t)
	m.handleDoctor("all")
	if m.status != diagnostics.Usage {
		t.Fatalf("status %q", m.status)
	}
}

func TestDoctorFixListsHandles(t *testing.T) {
	m := testModel(t)
	m.handleDoctor("fix")
	if m.overlay != overlayDoctor {
		t.Fatal("overlay")
	}
	found := map[string]bool{}
	for _, it := range m.items {
		found[it.Key] = true
	}
	for _, h := range []string{"ssh-wrap", "tmux-clipboard", "dcs-passthrough", "tmux-extended-keys"} {
		if !found[h] {
			t.Fatalf("missing %s in %+v", h, m.items)
		}
	}
}

func TestDoctorFixUnknownID(t *testing.T) {
	m := testModel(t)
	m.handleDoctor("fix nope")
	if !strings.Contains(m.status, diagnostics.Usage) || !strings.Contains(m.status, "unknown doctor fix") {
		t.Fatalf("%q", m.status)
	}
}

func TestDoctorFixConfirmsThenApplies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("BYOBU", "")
	t.Setenv("BYOBU_BACKEND", "")
	t.Setenv("BYOBU_CONFIG_DIR", "")
	m := testModel(t)
	m.sess = &agent.Session{ID: "s"}
	m.handleDoctor("fix tmux-clipboard")
	if m.overlay != overlayConfirm || m.confirmKind != "doctor-fix:tmux-clipboard" {
		t.Fatalf("confirm %s overlay %d", m.confirmKind, m.overlay)
	}
	body := doctorConfirmBody(m.confirmKind)
	if !strings.Contains(body, "set -g set-clipboard on") {
		t.Fatalf("body %q", body)
	}
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].body, "set -g set-clipboard on") {
		t.Fatalf("status %q blocks %+v", m.status, m.blocks)
	}
	data, err := os.ReadFile(home + "/.tmux.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "set -g set-clipboard on") {
		t.Fatalf("%s", data)
	}
}

func TestRecapEmptySessionToastsNoMessages(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s1", Messages: nil}
	if cmd := m.sendRecap(); cmd != nil {
		t.Fatal("must not sample")
	}
	if m.status != "No messages yet" {
		t.Fatalf("status %q", m.status)
	}
}

func TestRecapNoSessionNotice(t *testing.T) {
	m := testModel(t)
	m.sess = nil
	m.sendRecap()
	if m.status != "No active session" {
		t.Fatalf("%q", m.status)
	}
}

func TestRecapDisabledNotice(t *testing.T) {
	m := testModel(t)
	off := false
	m.cfg.Features.SessionRecap = &off
	m.sess = &agent.Session{ID: "s1", Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}}}
	m.sendRecap()
	if m.status != "Session recap is not enabled" {
		t.Fatalf("%q", m.status)
	}
}

func TestRecapDoesNotAppendUserMessage(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{
		ID: "s1",
		Messages: []types.Message{
			{Role: types.RoleUser, Content: "fix the parser"},
			{Role: types.RoleAssistant, Content: "done"},
		},
	}
	before := len(m.sess.Messages)
	_ = m.sendRecap()
	if len(m.sess.Messages) != before {
		t.Fatal("recap must not persist a user turn")
	}
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != blockSystem || m.blocks[len(m.blocks)-1].body != "Recap — generating…" {
		t.Fatalf("%+v", m.blocks)
	}
	m.applyRecapDone(recapDoneMsg{body: "Recap: We fixed the parser in foo.rs."})
	if m.blocks[len(m.blocks)-1].body != "Recap — We fixed the parser in foo.rs." {
		t.Fatalf("%q", m.blocks[len(m.blocks)-1].body)
	}
}

func TestQuestionCardRadioAndOther(t *testing.T) {
	m := testModel(t)
	m.question = &qAskMsg{Question: "Ship it?", Options: []string{"yes", "no"}, Reply: make(chan string, 1)}
	view := stripANSI(m.renderQuestion(60))
	if strings.Contains(view, "Question") && strings.Contains(view, "Question 1/") {
		t.Fatalf("card must not use a Question N/M title:\n%s", view)
	}
	if !strings.Contains(view, "Ship it?") {
		t.Fatalf("missing label:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("missing accent bar")
	}
	if !strings.Contains(view, "(○)") {
		t.Fatalf("radio chrome missing: %s", view)
	}
	if strings.Contains(view, "(●)") {
		t.Fatalf("nothing selected yet, should be empty radios:\n%s", view)
	}
	if !strings.Contains(view, questionFreeformPlaceholder) || strings.Contains(view, "z  free text") {
		t.Fatalf("freeform row: %s", view)
	}
	m.qCursor = 2
	m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.qTyping {
		t.Fatal("enter on freeform starts typing")
	}
}

func TestQuestionWrapsMultipleQuestions(t *testing.T) {
	m := testModel(t)
	m.question = &qAskMsg{
		Questions: []questionItem{
			{Question: "Color?", Options: []string{"red", "blue"}},
			{Question: "Size?", Options: []string{"s", "m"}},
		},
		Reply: make(chan string, 1),
	}
	view := stripANSI(m.renderQuestion(60))
	if strings.Contains(view, "Question 1/2") {
		t.Fatalf("question card has no N/M title:\n%s", view)
	}
	if !strings.Contains(view, "Color?") {
		t.Fatalf("%s", view)
	}
	m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyRight})
	if m.question.Index != 1 {
		t.Fatalf("index %d", m.question.Index)
	}
	view = stripANSI(m.renderQuestion(60))
	if !strings.Contains(view, "Size?") || strings.Contains(view, "Question 2/2") {
		t.Fatalf("%s", view)
	}
}

func TestAskUserJSONPayload(t *testing.T) {
	raw := `[{"question":"A?","options":["1"]},{"question":"B?","options":["2"]}]`
	got, ok := tools.DecodeAskUserJSON(tools.AskUserJSONPrefix + raw)
	if !ok || len(got) != 2 || got[1].Question != "B?" {
		t.Fatalf("%v %v", ok, got)
	}
}

func TestPromptSuggestionGhostAndKeys(t *testing.T) {
	m := testModel(t)
	m.suggest.enabled = true
	m.suggest.full = "run the tests"
	m.screen = screenChat
	m.overlay = overlayNone
	if g := m.suggest.ghostFor(""); g != "run the tests" {
		t.Fatalf("ghost %q", g)
	}
	if g := m.suggest.ghostFor("run "); g != "the tests" {
		t.Fatalf("prefix %q", g)
	}
	view := m.renderPromptInput()
	if !strings.Contains(view, "run the tests") {
		t.Fatalf("render %s", view)
	}
	if !m.tryAcceptSuggestion(true) {
		t.Fatal("ctrl+right")
	}
	if m.ta.Value() != "run" {
		t.Fatalf("one word %q", m.ta.Value())
	}
	m.ta.Reset()
	m.suggest.full = "run the tests"
	m.suggest.dismissed = false
	if !m.tryAcceptSuggestion(false) {
		t.Fatal("tab")
	}
	if m.ta.Value() != "run the tests" {
		t.Fatalf("accept %q", m.ta.Value())
	}
	m.suggest.full = "commit this"
	m.suggest.dismissed = false
	m.ta.Reset()
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.suggest.ghostFor("") != "" {
		t.Fatal("esc dismisses")
	}
}

func TestPromptSuggestionSanitize(t *testing.T) {
	if sanitizeSuggestion("NONE") != "" {
		t.Fatal("none")
	}
	if sanitizeSuggestion("run the tests") != "run the tests" {
		t.Fatal("ok")
	}
	if sanitizeSuggestion("Let me check") != "" {
		t.Fatal("agent voice")
	}
}

func TestShouldFetchSuggestionGates(t *testing.T) {
	m := testModel(t)
	m.suggest.enabled = true
	m.sess = &agent.Session{Messages: []types.Message{
		{Role: types.RoleUser, Content: "fix it"},
		{Role: types.RoleAssistant, Content: "done"},
	}}
	if !m.shouldFetchSuggestion(nil) {
		t.Fatal("want fetch")
	}
	m.ta.SetValue("draft")
	if m.shouldFetchSuggestion(nil) {
		t.Fatal("draft")
	}
	m.ta.Reset()
	m.queue = []string{"next"}
	if m.shouldFetchSuggestion(nil) {
		t.Fatal("queue")
	}
}
