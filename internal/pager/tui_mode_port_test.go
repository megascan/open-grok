package pager

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/config"
)

func TestPlanEmptyIsIdempotentOn(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/plan")
	if m.permMode != permPlan || !m.sess.Plan.Active {
		t.Fatal("empty /plan must enter plan mode")
	}
	if lastSys(m) != saveSuccessToast("Plan mode", true) {
		t.Fatalf("on toast %q", lastSys(m))
	}
	m.handleSlash("/plan")
	if m.permMode != permPlan || !m.sess.Plan.Active {
		t.Fatal("second empty /plan must stay in plan mode")
	}
	if lastSys(m) != saveSuccessToast("Plan mode", true) {
		t.Fatalf("idempotent toast %q", lastSys(m))
	}
}

func TestShiftTabCyclesAskPlanAutoAlways(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	if m.permMode != permAsk {
		t.Fatalf("start %d", m.permMode)
	}
	m.cyclePermMode()
	if m.permMode != permPlan || !m.sess.Plan.Active {
		t.Fatal("Ask → Plan")
	}
	if m.modeBanner != "Switched to mode: Plan" {
		t.Fatalf("banner %q", m.modeBanner)
	}
	m.cyclePermMode()
	if m.permMode != permAuto || m.sess.Plan.Active {
		t.Fatal("Plan → Auto must exit plan")
	}
	if m.modeBanner != "Switched to mode: Auto" {
		t.Fatalf("banner %q", m.modeBanner)
	}
	m.cyclePermMode()
	if m.permMode != permAlways || !m.sess.YOLO {
		t.Fatal("Auto → Always-approve")
	}
	if m.modeBanner != "Switched to mode: Always-Approve" {
		t.Fatalf("banner %q", m.modeBanner)
	}
	m.cyclePermMode()
	if m.permMode != permAsk || m.sess.YOLO {
		t.Fatal("Always-approve → Ask")
	}
	if m.modeBanner != "Switched to mode: Normal" {
		t.Fatalf("banner %q", m.modeBanner)
	}
}

func TestShiftTabSkipsAutoWhenGatedOff(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.autoModeGate = false
	m.cyclePermMode()
	if m.permMode != permPlan {
		t.Fatal("Ask → Plan")
	}
	m.cyclePermMode()
	if m.permMode != permAlways || m.sess.Plan.Active {
		t.Fatal("Plan → Always-approve when auto is gated")
	}
}

func TestAlwaysApproveSlashDoesNotExitPlanOnCyclePath(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/plan")
	m.handleSlash("/always-approve")
	if !m.sess.Plan.Active || m.permMode != permAlways {
		t.Fatal("slash YOLO keeps the plan gate")
	}
	m.cyclePermMode()
	if m.permMode != permAsk || m.sess.Plan.Active {
		t.Fatal("Shift+Tab from plan+yolo resets to Ask")
	}
}

func TestRememberEmptyEntersHashMode(t *testing.T) {
	m := testModel(t)
	m.screen = screenChat
	m.focusScroll = false
	m.handleSlash("/remember")
	if !m.rememberMode {
		t.Fatal("empty /remember enters remember mode")
	}
	if m.ta.Prompt != rememberPrefix {
		t.Fatalf("prefix %q", m.ta.Prompt)
	}
	m.ta.SetValue("keep the entropy home isolated")
	cmd, handled := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	_ = cmd
	if !handled {
		t.Fatal("enter must send the note")
	}
	if m.rememberMode {
		t.Fatal("enter exits remember mode")
	}
	if lastSys(m) == "Please provide a memory note." {
		t.Fatal("note should save")
	}
}

func TestHashOnEmptyPromptRequiresFeatureFlag(t *testing.T) {
	m := testModel(t)
	m.screen = screenChat
	m.focusScroll = false
	cmd, handled := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'#'}})
	_ = cmd
	if handled && m.rememberMode {
		t.Fatal("# is gated off by default")
	}
	m.cfg.Prefs = config.Prefs{"features.remember_mode": "true"}
	cmd, handled = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'#'}})
	_ = cmd
	if !handled || !m.rememberMode {
		t.Fatal("# on an empty prompt must enter remember mode when enabled")
	}
	if m.ta.Prompt != rememberPrefix {
		t.Fatalf("prefix %q", m.ta.Prompt)
	}
}

func TestQueueSlashCommitsSystemBlock(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/queue")
	if m.overlay == overlayQueue {
		t.Fatal("/queue must not open the centered overlay")
	}
	if lastSys(m) != "Queue is empty." {
		t.Fatalf("empty %q", lastSys(m))
	}
	m.queue = []string{"first prompt", "second\nwith two lines"}
	m.handleSlash("/queue")
	text := lastSys(m)
	if !strings.Contains(text, "Queued prompts (2):") {
		t.Fatalf("header %q", text)
	}
	if !strings.Contains(text, "#1  first prompt") {
		t.Fatalf("row1 %q", text)
	}
	if !strings.Contains(text, "#2  second  (+1 more line)") {
		t.Fatalf("row2 %q", text)
	}
}

func TestTasksSlashCommitsSystemBlock(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/tasks")
	if m.overlay == overlayTasks {
		t.Fatal("/tasks must not open the centered overlay")
	}
	if lastSys(m) != "No background tasks, workflows, or subagents." {
		t.Fatalf("empty %q", lastSys(m))
	}
}

func TestSettingsFilterRequiresSlashOrI(t *testing.T) {
	m := testModel(t)
	m.openSettings()
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.filter != "" {
		t.Fatal("browse must not filter on random runes")
	}
	body := stripANSI(m.renderSettings(72))
	if !strings.Contains(body, "/ search") || !strings.Contains(body, "F2/Esc close") {
		t.Fatalf("browse hints:\n%s", body)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.settingsFilter {
		t.Fatal("/ enters filter")
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !strings.Contains(m.filter, "c") {
		t.Fatalf("filter %q", m.filter)
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.settingsFilter || m.filter != "" {
		t.Fatal("esc in filter returns to browse and clears")
	}
	if m.overlay != overlaySettings {
		t.Fatal("esc in filter must not close the modal")
	}
	m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Fatal("esc in browse closes")
	}
}

func TestSettingsF2TogglesModal(t *testing.T) {
	m := testModel(t)
	m.screen = screenChat
	m.focusScroll = false
	cmd, handled := m.handleKey(tea.KeyMsg{Type: tea.KeyF2})
	_ = cmd
	if !handled || m.overlay != overlaySettings {
		t.Fatal("F2 opens settings")
	}
	cmd, handled = m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyF2})
	_ = cmd
	if !handled || m.overlay != overlayNone {
		t.Fatal("F2 closes settings")
	}
}

func TestFeedbackCopyMatchesRust(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/feedback")
	if m.overlay != overlayFeedback {
		t.Fatal("bare /feedback opens the pane")
	}
	got := stripANSI(m.renderOverlay())
	if !strings.Contains(got, feedbackQuestionLabel) {
		t.Fatalf("question:\n%s", got)
	}
	m.feedback = "the pager needs more cowbell"
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	if lastSys(m) != feedbackThanks {
		t.Fatalf("thanks %q", lastSys(m))
	}
	m.handleSlash("/feedback ship it")
	if lastSys(m) != feedbackThanks {
		t.Fatalf("inline %q", lastSys(m))
	}
}

func TestYolo2AutoSwarmCopy(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/yolo-2")
	if lastSys(m) != yolo2SandboxError && m.status != yolo2SandboxError {
		t.Fatalf("unsandboxed %q status %q", lastSys(m), m.status)
	}
	m.handleSlash("/auto")
	if lastSys(m) != permissionModeToast(permAuto) && m.status != permissionModeToast(permAuto) {
		t.Fatalf("auto on %q status %q", lastSys(m), m.status)
	}
	m.permMode = permAuto
	m.handleSlash("/auto")
	if lastSys(m) != permissionModeToast(permAsk) && m.status != permissionModeToast(permAsk) {
		t.Fatalf("auto off %q status %q", lastSys(m), m.status)
	}
	m.handleSlash("/swarm")
	if lastSys(m) != saveSuccessToast("Swarm mode", m.swarm) && m.status != saveSuccessToast("Swarm mode", m.swarm) {
		t.Fatalf("swarm %q status %q", lastSys(m), m.status)
	}
}
