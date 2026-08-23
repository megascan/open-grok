package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/hooks"
	"github.com/megascan/open-grok/internal/types"
)

func TestPermissionRadioCardBashScope(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	ch := make(chan bool, 1)
	m.perm = &permAskMsg{
		Tool: "bash", Access: "bash",
		Command: "cargo test --workspace -- --nocapture",
		Reply:   ch,
	}
	m.initPermCard()
	labels := m.permChoices()
	if len(labels) < 4 {
		t.Fatalf("choices %+v", labels)
	}
	if labels[0].ID != "always-cmd" || !strings.Contains(labels[0].Label, "Always allow:") {
		t.Fatalf("first row %q", labels[0].Label)
	}
	if labels[1].ID != "allow" {
		t.Fatalf("yes proceed should be second, got %s", labels[1].ID)
	}
	if labels[2].ID != "never-cmd" || !strings.Contains(labels[2].Label, "Never allow:") {
		t.Fatalf("never-cmd %q", labels[2].Label)
	}
	if labels[len(labels)-1].ID != "followup" {
		t.Fatal("followup last")
	}
	m.permWordN = 2
	scoped := m.permScopedCmd()
	if scoped != "cargo test" {
		t.Fatalf("scope %q", scoped)
	}
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyLeft})
	if m.permScopedCmd() != "cargo" {
		t.Fatalf("left should shrink to cargo, got %q", m.permScopedCmd())
	}
	m.permCursor = 0
	m.applyPermChoice(permChoice{ID: "always-cmd"})
	if !<-ch {
		t.Fatal("always-cmd must allow")
	}
	found := false
	for _, r := range m.sess.Allow {
		if r == "bash:cargo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bash:cargo in %v", m.sess.Allow)
	}
}

func TestPermissionFollowupQueuesText(t *testing.T) {
	m := testModel(t)
	ch := make(chan bool, 1)
	m.perm = &permAskMsg{Tool: "edit", Access: "edit", Reply: ch}
	m.initPermCard()
	m.permFollowupMode = true
	m.permFollowup = "use a smaller patch"
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyEnter})
	if <-ch {
		t.Fatal("followup denies")
	}
	if len(m.queue) != 1 || m.queue[0] != "use a smaller patch" {
		t.Fatalf("queue %v", m.queue)
	}
}

func TestRewindPickerTruncatesToSelectedTurn(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.sess.Messages = []types.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "first prompt"},
		{Role: types.RoleAssistant, Content: "first reply"},
		{Role: types.RoleUser, Content: "second prompt"},
		{Role: types.RoleAssistant, Content: "second reply"},
	}
	_ = m.sess.Persist()
	m.cfg.UI.ConfirmRewind = false
	m.openRewindPicker()
	if m.overlay != overlayRewind {
		t.Fatalf("overlay %d", m.overlay)
	}
	if len(m.rewindPoints) != 2 {
		t.Fatalf("points %d", len(m.rewindPoints))
	}
	if m.rewindPoints[0].Preview != "first prompt" {
		t.Fatalf("preview %q", m.rewindPoints[0].Preview)
	}
	m.rewindCursor = 0
	m.applyRewindChoice()
	if len(m.sess.Messages) != 1 || m.sess.Messages[0].Role != types.RoleSystem {
		t.Fatalf("messages %+v", m.sess.Messages)
	}
	if strings.TrimSpace(m.ta.Value()) != "first prompt" {
		t.Fatalf("draft %q", m.ta.Value())
	}
}

func TestRewindConfirmNeverAsk(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "hello"},
		{Role: types.RoleAssistant, Content: "hi"},
	}
	m.cfg.UI.ConfirmRewind = true
	m.openRewindPicker()
	m.applyRewindChoice()
	if m.rewindPhase != rewindConfirm {
		t.Fatalf("phase %d", m.rewindPhase)
	}
	m.rewindCursor = 1
	m.applyRewindChoice()
	if m.cfg.UI.ConfirmRewind {
		t.Fatal("don't ask again should persist off")
	}
}

func TestBtwComposerOpensWithoutInterrupting(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.busy = true
	m.handleSlash("/btw")
	if m.overlay != overlayBtw {
		t.Fatalf("overlay %d", m.overlay)
	}
	body := m.renderBtwComposer(60)
	if !strings.Contains(body, "without interrupting") {
		t.Fatalf("composer copy:\n%s", body)
	}
	m.btwDraft = "what files did we touch?"
	m.handleBtwComposerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.btw == nil || m.btw.Question != "what files did we touch?" {
		t.Fatalf("btw %+v", m.btw)
	}
	if m.busy != true {
		t.Fatal("btw must not clear the main turn busy flag")
	}
}

func TestMermaidFlowchartASCII(t *testing.T) {
	src := "flowchart TD\n  A[Start] --> B{Decision}\n  B -->|yes| C[OK]\n"
	out := renderMermaidASCII(src, 72)
	if !strings.Contains(out, "Start") || !strings.Contains(out, "Decision") {
		t.Fatalf("missing node labels:\n%s", out)
	}
	if !strings.Contains(out, "mermaid") {
		t.Fatal("card title")
	}
	md := renderMarkdownEx("```mermaid\n"+src+"\n```", 72, true)
	if !strings.Contains(md, "Start") {
		t.Fatalf("markdown fence:\n%s", md)
	}
	off := renderMarkdownEx("```mermaid\n"+src+"\n```", 72, false)
	if !strings.Contains(off, "render_mermaid off") {
		t.Fatalf("off path:\n%s", off)
	}
}

func TestMCPOverlayListsLocalServers(t *testing.T) {
	m := testModel(t)
	m.cfg.MCP = map[string]config.MCP{"docs": {Command: "npx", Args: []string{"-y", "docs"}}}
	home := t.TempDir()
	t.Setenv("ENTROPY_HOME", home)
	config.ResetHomeForTest()
	m.cfg.MCP = map[string]config.MCP{"docs": {Command: "npx", Args: []string{"-y", "docs"}}}
	if err := config.Save(m.cfg); err != nil {
		t.Fatal(err)
	}
	m.openMCP()
	if m.overlay != overlayMCP {
		t.Fatalf("overlay %d", m.overlay)
	}
	foundLocal, foundServer := false, false
	for _, it := range m.items {
		if strings.Contains(it.Label, "Local (") {
			foundLocal = true
		}
		if strings.HasPrefix(it.Key, "mcp:docs") {
			foundServer = true
		}
	}
	if !foundLocal || !foundServer {
		t.Fatalf("items %+v", m.items)
	}
}

func TestHooksTabListsImportedJSON(t *testing.T) {
	m := testModel(t)
	dir := filepath.Join(m.opts.CWD, ".entropy", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo deny"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "imported-from-claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := hooks.ListImported(m.opts.CWD)
	if len(got) == 0 || got[0].Matcher != "Bash" {
		t.Fatalf("%+v", got)
	}
	m.openExtensions(0)
	found := false
	for _, it := range m.items {
		if strings.Contains(it.Label, "Bash") {
			found = true
		}
	}
	if !found {
		t.Fatalf("extensions items %+v", m.items)
	}
}

func TestBlockViewerOpensFromPermission(t *testing.T) {
	m := testModel(t)
	m.perm = &permAskMsg{Tool: "bash", Access: "bash", Args: `{"command":"ls"}`}
	m.openBlockViewer("Permission args", m.perm.Args)
	if m.overlay != overlayBlock {
		t.Fatal("overlay")
	}
	m.handleBlockViewerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != overlayPermission {
		t.Fatalf("esc returns to permission, got %d", m.overlay)
	}
}
