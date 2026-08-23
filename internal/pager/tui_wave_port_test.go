package pager

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestPrepareBashDisplayPreservesContinuations(t *testing.T) {
	raw := "docker run \\\n  -v /tmp:/tmp \\\n  -e FOO=bar \\\n  alpine:latest\n"
	prepared := prepareBashDisplayText(raw)
	if !strings.Contains(prepared, "docker run \\\n  -v /tmp:/tmp \\\n  -e FOO=bar \\\n  alpine:latest") {
		t.Fatalf("got %q", prepared)
	}
	if strings.Contains(prepared, "docker run \\  -v") {
		t.Fatal("flattened continuation")
	}
	if strings.Count(prepared, "\n")+1 != 4 && len(strings.Split(prepared, "\n")) != 4 {
		t.Fatalf("lines %d %q", len(strings.Split(prepared, "\n")), prepared)
	}
}

func TestPrepareBashDisplayDropsDanglingContinuationNewline(t *testing.T) {
	if got := prepareBashDisplayText("echo a \\\n"); got != "echo a \\" {
		t.Fatalf("%q", got)
	}
	if got := prepareBashDisplayText("echo a \\\n\n"); got != "echo a \\" {
		t.Fatalf("%q", got)
	}
	if got := prepareBashDisplayText("a \\\nb\n"); got != "a \\\nb" {
		t.Fatalf("%q", got)
	}
}

func TestPrepareBashDisplayNormalizesCRLF(t *testing.T) {
	if got := prepareBashDisplayText("echo a\r\necho b\r\n"); got != "echo a\necho b" {
		t.Fatalf("%q", got)
	}
}

func TestBashWordsKeepQuotedArgsAndSkipHeredocBody(t *testing.T) {
	words := bashWords(`echo "hello world"`)
	if len(words) != 2 || words[0] != "echo" || words[1] != "hello world" {
		t.Fatalf("%q", words)
	}
	script := "cat <<EOF\nhello world\nmore text\nEOF\n"
	words = bashWords(script)
	joined := strings.Join(words, " ")
	if strings.Contains(joined, "hello world") || strings.Contains(joined, "more text") {
		t.Fatalf("heredoc body leaked into scope words: %q", words)
	}
	if len(words) == 0 || words[0] != "cat" {
		t.Fatalf("expected cat opener, got %q", words)
	}
}

func TestPermissionCardKeepsHeredocLines(t *testing.T) {
	m := testModel(t)
	m.perm = &permAskMsg{
		Tool: "bash", Access: "bash",
		Command: "cat <<EOF\nhello world\nEOF\n",
		Reply:   make(chan bool, 1),
	}
	m.initPermCard()
	view := stripANSI(m.renderPermission(72))
	if !strings.Contains(view, "hello world") {
		t.Fatalf("missing heredoc body:\n%s", view)
	}
	if strings.Contains(view, "hello world EOF") {
		t.Fatal("space-joined heredoc")
	}
	if m.permScopedCmd() != "cat <<EOF" && !strings.HasPrefix(m.permScopedCmd(), "cat") {
		t.Fatalf("scope %q", m.permScopedCmd())
	}
}

func TestPermissionCtrlFExpandsCollapsedScript(t *testing.T) {
	m := testModel(t)
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, "echo line")
	}
	m.perm = &permAskMsg{Tool: "bash", Access: "bash", Command: strings.Join(lines, "\n"), Reply: make(chan bool, 1)}
	m.initPermCard()
	view := stripANSI(m.renderPermission(72))
	if !strings.Contains(view, "Ctrl-F") || !strings.Contains(view, "to expand") {
		t.Fatalf("missing expand hint:\n%s", view)
	}
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	if !m.permExpanded {
		t.Fatal("ctrl+f should expand")
	}
	expanded := stripANSI(m.renderPermission(72))
	if strings.Contains(expanded, "to expand") {
		t.Fatalf("expanded still truncated:\n%s", expanded)
	}
}

func TestFmtPct5MatchesRust(t *testing.T) {
	if fmtPct5(0) != "0.00%" || fmtPct5(5.123) != "5.12%" || fmtPct5(9.99) != "9.99%" {
		t.Fatal("under 10")
	}
	if fmtPct5(10) != "10.0%" || fmtPct5(20.16) != "20.2%" || fmtPct5(99.9) != "99.9%" {
		t.Fatal("10-99")
	}
	if fmtPct5(100) != "MAX %" || fmtPct5(150) != "MAX %" {
		t.Fatal("max")
	}
	for _, pct := range []float64{0, 0.01, 1, 5.55, 9.99, 10, 50, 99.9, 100} {
		if n := utf8.RuneCountInString(fmtPct5(pct)); n != 5 {
			t.Fatalf("fmtPct5(%v)=%q width %d", pct, fmtPct5(pct), n)
		}
	}
}

func TestContextBarHoverKeepsWidth(t *testing.T) {
	used, total := 8500, 1_000_000
	plain := contextBarNatural(used, total)
	hovered := stripANSI(contextBarText(used, total, true))
	idle := stripANSI(contextBarText(used, total, false))
	if utf8.RuneCountInString(idle) != utf8.RuneCountInString(plain) {
		t.Fatalf("idle %q vs %q", idle, plain)
	}
	if utf8.RuneCountInString(hovered) != utf8.RuneCountInString(plain) {
		t.Fatalf("hover %q vs %q", hovered, plain)
	}
	if !strings.Contains(hovered, "%") && !strings.Contains(hovered, "MAX") {
		t.Fatalf("hover missing pct: %q", hovered)
	}
}

func TestHeaderUsesContextBarSeparator(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "abcdef12-xxxx"}
	m.opts.Model.ContextWindow = 1_000_000
	head := stripANSI(m.renderHeader())
	if !strings.Contains(head, contextBarSep) {
		t.Fatalf("missing separator:\n%s", head)
	}
	if strings.Contains(head, "Context:") {
		t.Fatalf("legacy Context: label still present:\n%s", head)
	}
	m.ctxHover = true
	hover := stripANSI(m.renderHeader())
	if !strings.Contains(hover, "%") && !strings.Contains(hover, "MAX") {
		t.Fatalf("hover bar:\n%s", hover)
	}
}

func TestCancelTurnRendersRadioChrome(t *testing.T) {
	m := testModel(t)
	m.cancel = &cancelTurnState{Running: 2}
	view := stripANSI(m.renderCancelTurn(60))
	if !strings.Contains(view, "(●)") || !strings.Contains(view, "(○)") {
		t.Fatalf("radio chrome:\n%s", view)
	}
	if strings.Contains(view, "▸ Stop running") {
		t.Fatal("legacy triangle marker")
	}
	for _, c := range cancelTurnChoices {
		if !strings.Contains(view, c) {
			t.Fatalf("missing %q in %s", c, view)
		}
	}
}

func TestShareAndSessionInfoRequireSession(t *testing.T) {
	m := testModel(t)
	m.sess = nil
	m.handleSlash("/share")
	if m.status != "No active session to share" {
		t.Fatalf("share %q", m.status)
	}
	m.handleSlash("/session-info")
	if m.status != "No active session" {
		t.Fatalf("session-info %q", m.status)
	}
	m.handleSlash("/tasks")
	if m.status != "No active session" {
		t.Fatalf("tasks %q", m.status)
	}
}

func TestSessionInfoLayoutMatchesRust(t *testing.T) {
	m := testModel(t)
	m.opts.Model = catalog.Model{ID: "grok-4", Name: "Grok 4", Provider: types.ProviderOpenRouter, APIBackend: types.BackendChat, ContextWindow: 128000}
	m.sess = &agent.Session{
		ID: "test-session-id", Title: "fix parser", CWD: "/tmp/work",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello world"}},
	}
	text := m.sessionInfo()
	for _, want := range []string{
		"  Title: fix parser",
		"  Shell version:",
		"  Session ID: test-session-id",
		"  Working directory: /tmp/work",
		"  Model: Grok 4",
		"  API Backend:",
		"  Turn: 1",
		"  Context:",
		"tokens",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestCacheOverlayEmptyAndTelemetryHeader(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s"}
	m.openCache()
	if m.overlay != overlayCache {
		t.Fatal("overlay")
	}
	empty := m.cacheReport()
	if empty != "Prompt cache telemetry: no turns recorded yet in this session." {
		t.Fatalf("%q", empty)
	}
	m.sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "hello there"},
		{Role: types.RoleAssistant, Content: "hi"},
		{Role: types.RoleUser, Content: "hello again"},
	}
	body := m.cacheReport()
	if !strings.Contains(body, "Prompt Cache Telemetry & Diagnostics:") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, "All-provider hit rate:") || !strings.Contains(body, "Turns tracked:") {
		t.Fatalf("%s", body)
	}
	view := stripANSI(m.renderCache(72))
	if !strings.Contains(view, "Prompt cache") {
		t.Fatalf("%s", view)
	}
}

func TestTasksOverlayCombinesSubagentsAndLoopsRunningFirst(t *testing.T) {
	m := testModel(t)
	m.sess = &agent.Session{ID: "s", Team: tools.NewTeamBox()}
	m.sess.Team.Register("child-b", "worker-b")
	m.sess.Team.Register("child-a", "worker-a")
	m.sess.Team.Finish("child-a", "done", false)
	m.loops = []loopJob{
		{ID: "loop-1", Prompt: "check deploy", Every: time.Minute, stopped: true},
		{ID: "loop-2", Prompt: "poll ci", Every: 2 * time.Minute},
	}
	m.openTasks()
	if m.overlay != overlayTasks {
		t.Fatal("overlay")
	}
	if len(m.items) != 4 {
		t.Fatalf("items %+v", m.items)
	}
	for i, it := range m.items {
		isRun := strings.Contains(it.Detail, "running")
		if i < 2 && !isRun {
			t.Fatalf("running-first broken at %d: %+v", i, m.items)
		}
		if i >= 2 && isRun {
			t.Fatalf("idle after running broken at %d: %+v", i, m.items)
		}
	}
	labels := ""
	for _, it := range m.items {
		labels += it.Label + " "
	}
	if !strings.Contains(labels, "Task loop-2") || !strings.Contains(labels, "worker-a") || !strings.Contains(labels, "worker-b") {
		t.Fatalf("combined list %s", labels)
	}
	title := m.overlayTitle()
	if title != "Tasks" {
		t.Fatalf("title %q", title)
	}
}

func TestHighlightBashKeepsPhysicalText(t *testing.T) {
	src := "if true; then echo \"hi\" # c\nfi"
	got := stripANSI(highlightBashCommand(src))
	if got != src {
		t.Fatalf("flatten %q vs %q", got, src)
	}
}
