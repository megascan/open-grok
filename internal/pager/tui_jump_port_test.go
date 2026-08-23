package pager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func TestParseCopyArgMatchesRust(t *testing.T) {
	n, path, err := parseCopyArg("")
	if err != nil || n != 1 || path != "" {
		t.Fatalf("empty: %d %q %v", n, path, err)
	}
	n, path, err = parseCopyArg("2")
	if err != nil || n != 2 || path != "" {
		t.Fatalf("2: %d %q %v", n, path, err)
	}
	n, path, err = parseCopyArg("out.txt")
	if err != nil || n != 1 || path != "out.txt" {
		t.Fatalf("path: %d %q %v", n, path, err)
	}
	n, path, err = parseCopyArg("2 out.txt")
	if err != nil || n != 2 || path != "out.txt" {
		t.Fatalf("n path: %d %q %v", n, path, err)
	}
	n, path, err = parseCopyArg("2 ~/exports/my note.txt")
	if err != nil || n != 2 || path != "~/exports/my note.txt" {
		t.Fatalf("spaces: %d %q %v", n, path, err)
	}
	if _, _, err = parseCopyArg("0"); err == nil || !strings.Contains(err.Error(), "Usage: /copy") {
		t.Fatalf("zero: %v", err)
	}
	n, path, err = parseCopyArg("-1")
	if err != nil || n != 1 || path != "-1" {
		t.Fatalf("neg is path: %d %q %v", n, path, err)
	}
}

func TestCopyNthAssistantToFileNotFullExport(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s"}
	m.blocks = []block{
		{kind: blockUser, body: "hi"},
		{kind: blockAgent, body: "first reply"},
		{kind: blockUser, body: "again"},
		{kind: blockAgent, body: "second reply"},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if cmd := m.copyAssistant(1, path); cmd != nil {
		t.Fatal("file copy should not emit clipboard cmd")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second reply" {
		t.Fatalf("wrote %q", got)
	}
	path2 := filepath.Join(dir, "second.txt")
	_ = m.copyAssistant(2, path2)
	got, _ = os.ReadFile(path2)
	if string(got) != "first reply" {
		t.Fatalf("nth 2 wrote %q", got)
	}
}

func TestCopyNoSessionAndMissingAssistant(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/copy")
	if m.status != "No active session" {
		t.Fatalf("status %q", m.status)
	}
	m.sess = &agent.Session{ID: "s"}
	m.handleSlash("/copy")
	if last := m.blocks[len(m.blocks)-1]; last.body != "No assistant messages to copy" {
		t.Fatalf("%q", last.body)
	}
}

func TestParseForkArgs(t *testing.T) {
	a, err := parseForkArgs("")
	if err != nil || a.worktreeOverride != nil || a.directive != "" {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = parseForkArgs("--worktree ship it")
	if err != nil || a.worktreeOverride == nil || !*a.worktreeOverride || a.directive != "ship it" {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = parseForkArgs("--no-worktree")
	if err != nil || a.worktreeOverride == nil || *a.worktreeOverride {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err = parseForkArgs("--at 3"); err == nil || err.Error() != "--at is not supported in this version" {
		t.Fatalf("%v", err)
	}
	if _, err = parseForkArgs("--worktree --no-worktree"); err == nil {
		t.Fatal("expected exclusive")
	}
	a, err = parseForkArgs("--foo bar")
	if err != nil || a.directive != "--foo bar" || a.worktreeOverride != nil {
		t.Fatalf("unknown flag is directive: %+v %v", a, err)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestForkNoSessionAndQuestion(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/fork")
	if m.status != "No active session" {
		t.Fatalf("%q", m.status)
	}
	gitInit(t, m.opts.CWD)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/fork")
	if m.overlay != overlayQuestion || m.question == nil {
		t.Fatal("expected worktree question")
	}
	if m.question.Question != "Run this fork in an isolated git worktree?" {
		t.Fatalf("%q", m.question.Question)
	}
	if got := strings.Join(m.question.Options, "|"); got != "Yes|No|Always worktree|Never worktree" {
		t.Fatalf("options %s", got)
	}
	view := stripANSI(m.renderQuestion(72))
	if strings.Contains(view, "Other") || strings.Contains(view, questionFreeformPlaceholder) {
		t.Fatalf("local fork question must not show freeform:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) || !strings.Contains(view, "(○)") {
		t.Fatalf("radio chrome:\n%s", view)
	}
}

func TestForkNoWorktreeFlagSkipsQuestion(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/fork --no-worktree")
	if m.overlay == overlayQuestion {
		t.Fatal("flag should skip question")
	}
}

func TestCompactNoSession(t *testing.T) {
	m := testModel(t)
	m.handleSlash("/compact")
	if m.status != "No active session" {
		t.Fatalf("%q", m.status)
	}
}

func TestDebugBareStatusAndUnknown(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s"}
	m.handleSlash("/debug")
	got := m.blocks[len(m.blocks)-1].body
	want := "debug toggles: scroll off · fps off · log off — toggle with /debug <scroll|fps|log>"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(got, "\u00b7") || !strings.Contains(got, "\u2014") {
		t.Fatal("must use middle dot and em dash")
	}
	m.handleSlash("/debug wat")
	err := m.blocks[len(m.blocks)-1].body
	if !strings.Contains(err, "wat") || !strings.Contains(err, "scroll") {
		t.Fatalf("%q", err)
	}
	m.handleSlash("/debug fps")
	if !m.fpsOn {
		t.Fatal("fps toggle")
	}
	m.handleSlash("/debug scroll")
	if !m.scrollHUD {
		t.Fatal("scroll toggle")
	}
	m.handleSlash("/scroll-debug")
	if m.scrollHUD {
		t.Fatal("/scroll-debug must share the same HUD bit")
	}
	m.handleSlash("/debug log")
	if m.scrollLogPath == "" || !strings.Contains(m.blocks[len(m.blocks)-1].body, "scroll log: recording to") {
		t.Fatalf("log on %q %q", m.scrollLogPath, m.blocks[len(m.blocks)-1].body)
	}
	m.handleSlash("/debug log")
	if m.scrollLogPath != "" || m.blocks[len(m.blocks)-1].body != "scroll log: off" {
		t.Fatalf("log off %q %q", m.scrollLogPath, m.blocks[len(m.blocks)-1].body)
	}
}

func TestDebugStatusOnOffWords(t *testing.T) {
	m := testModel(t)
	m.scrollHUD = true
	m.fpsOn = true
	m.scrollLogPath = "/tmp/x.jsonl"
	got := m.debugStatusLine()
	if !strings.Contains(got, "scroll on") || !strings.Contains(got, "fps on") || !strings.Contains(got, "log on") {
		t.Fatalf("%q", got)
	}
}

func TestJumpPickerLiveScrollAndEscRestore(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.layoutChat()
	m.blocks = []block{
		{kind: blockUser, body: strings.Repeat("first turn\n", 40)},
		{kind: blockAgent, body: strings.Repeat("a1\n", 40)},
		{kind: blockUser, body: strings.Repeat("second turn\n", 40)},
		{kind: blockAgent, body: strings.Repeat("a2\n", 40)},
	}
	m.refreshVP()
	m.vp.GotoBottom()
	origin := m.vp.YOffset
	m.openJump()
	if m.overlay != overlayTimeline {
		t.Fatal("overlay")
	}
	if m.overlayTitle() != "Jump to which turn?" {
		t.Fatalf("title %q", m.overlayTitle())
	}
	if len(m.items) != 2 || !strings.Contains(m.items[0].Label, "first turn") {
		t.Fatalf("%+v", m.items)
	}
	m.cursor = 0
	m.previewJump()
	if m.vp.YOffset == origin && origin != 0 {
		t.Fatal("preview should move viewport")
	}
	m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayNone {
		t.Fatal("esc close")
	}
	if m.vp.YOffset != origin {
		t.Fatalf("esc restore %d vs %d", m.vp.YOffset, origin)
	}
	m.openJump()
	m.cursor = 0
	m.pickOverlay()
	if m.overlay != overlayNone {
		t.Fatal("enter keeps jump and closes")
	}
}

func TestTimelineTogglesRailNotList(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s"}
	m.width = 80
	m.blocks = []block{
		{kind: blockUser, body: "one"},
		{kind: blockAgent, body: "a"},
		{kind: blockUser, body: "two"},
		{kind: blockAgent, body: "b"},
	}
	m.handleSlash("/timeline")
	if m.overlay != overlayNone {
		t.Fatal("/timeline must not open a list")
	}
	if !m.cfg.UI.ShowTimeline {
		t.Fatal("show_timeline on")
	}
	if !strings.Contains(m.blocks[len(m.blocks)-1].body, "Timeline sidebar: on") {
		t.Fatalf("%q", m.blocks[len(m.blocks)-1].body)
	}
	if w := m.timelineRailWidth(); w != 2 {
		t.Fatalf("rail width %d", w)
	}
	m.vp = m.vp
	m.vp.Height = 12
	rail := m.renderTimelineRail()
	if !strings.Contains(rail, "\u25b4") || !strings.Contains(rail, "\u25be") {
		t.Fatalf("chevrons:\n%s", rail)
	}
	m.handleSlash("/timeline")
	if m.cfg.UI.ShowTimeline {
		t.Fatal("toggled off")
	}
}

func TestTurnStatusLine(t *testing.T) {
	m := testModel(t)
	m.width = 80
	m.busy = true
	m.turnActivity = "thinking"
	m.turnStarted = time.Now().Add(-2 * time.Second)
	m.phaseStarted = m.turnStarted
	m.queue = []string{"follow up"}
	line := stripANSI(m.renderTurnStatus())
	if !strings.Contains(line, "Thinking…") {
		t.Fatalf("%q", line)
	}
	if !strings.Contains(line, "queued — Enter to send now") {
		t.Fatalf("queue hint %q", line)
	}
	if !strings.Contains(line, "[stop]") {
		t.Fatalf("stop %q", line)
	}
	m.turnActivity = "tool"
	m.turnToolTitle = "bash"
	line = stripANSI(m.renderTurnStatus())
	if !strings.Contains(line, "Run command") {
		t.Fatalf("%q", line)
	}
	m.busy = false
	m.turnActivity = ""
	m.queue = nil
	if m.renderTurnStatus() != "" && m.watcherCount() == 0 {
		t.Fatalf("idle should hide: %q", m.renderTurnStatus())
	}
}

func TestChatPrivacyBannerAndPlanPlaceholder(t *testing.T) {
	m := testModel(t)
	if err := auth.SaveAPIKey(types.ProviderXAI, "xai-test-not-real", false); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	m.width, m.height = 80, 24
	m.layoutChat()
	if !m.showPrivacyBanner() {
		t.Fatal("banner should show")
	}
	view := stripANSI(m.viewChat())
	if !strings.Contains(view, "Help improve Open Grok") {
		t.Fatalf("chat missing privacy banner:\n%s", view)
	}
	if !strings.Contains(view, "SpaceXAI") || !strings.Contains(view, "Read Terms and Privacy Policy") {
		t.Fatalf("privacy copy:\n%s", view)
	}
	if !strings.Contains(view, "[Opt in]") || !strings.Contains(view, "[Opt out]") {
		t.Fatalf("privacy buttons:\n%s", view)
	}
	m.plan = &planAskMsg{HasPlan: false, Plan: ""}
	body := stripANSI(m.renderPlanApproval(72))
	if !strings.Contains(body, "No plan written yet") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, "Approve") || !strings.Contains(body, "Request changes") {
		t.Fatalf("%s", body)
	}
}

func TestFPSStatsAndEnvGate(t *testing.T) {
	if formatFPSStats(nil) != "fps:- p50:- p95:-" {
		t.Fatal(formatFPSStats(nil))
	}
	samples := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	got := formatFPSStats(samples)
	if !strings.Contains(got, "fps:") || !strings.Contains(got, "p50:") || !strings.Contains(got, "p95:") {
		t.Fatalf("%q", got)
	}
	t.Setenv("ENTROPY_FPS", "1")
	if !envFlagOn("ENTROPY_FPS", "GROK_FPS") {
		t.Fatal("ENTROPY_FPS=1")
	}
	t.Setenv("ENTROPY_FPS", "0")
	if envFlagOn("ENTROPY_FPS", "GROK_FPS") {
		t.Fatal("0 is off")
	}
}

func TestAckPrivacyRelayouts(t *testing.T) {
	m := testModel(t)
	if err := auth.SaveAPIKey(types.ProviderXAI, "xai-test-not-real", false); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	if !m.showPrivacyBanner() {
		t.Fatal("show")
	}
	m.ackPrivacyBanner(false)
	if m.showPrivacyBanner() {
		t.Fatal("acked")
	}
	if m.cfg.Prefs["privacy_banner_acked"] != "true" {
		t.Fatal("persist")
	}
	_ = config.Home()
}
