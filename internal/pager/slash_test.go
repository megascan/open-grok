package pager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func TestParseGoalSlash(t *testing.T) {
	if parseGoalSlash("").Verb != goalVerbStatus {
		t.Fatal("empty is status")
	}
	if parseGoalSlash("pause").Verb != goalVerbPause {
		t.Fatal("pause")
	}
	got := parseGoalSlash("Migrate auth --budget 8000")
	if got.Verb != goalVerbStart || got.Objective != "Migrate auth" || got.Budget == nil || *got.Budget != 8000 {
		t.Fatalf("%+v", got)
	}
}

func TestMatchingSlashIncludesGoal(t *testing.T) {
	found := false
	for _, c := range matchingSlash("/go") {
		if c.Name == "goal" {
			found = true
		}
	}
	if !found {
		t.Fatal("/go should suggest /goal")
	}
}

func TestRenderMarkdownHeadersAndBullets(t *testing.T) {
	out := renderMarkdown("# Title\n- item\nplain `code`", 80)
	if !strings.Contains(stripANSI(out), "Title") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(stripANSI(out), "•") {
		t.Fatalf("bullets: %q", out)
	}
}

func TestRustBuiltinSlashCatalogResolves(t *testing.T) {
	required := []string{
		"quit", "help", "docs", "home", "delete", "new", "fork", "compact", "copy",
		"find", "history", "export", "transcript", "edit-prompt", "expand", "context",
		"minimal", "fullscreen", "model", "effort", "fast", "always-approve", "yolo-2",
		"auto", "multiline", "compact-mode", "vim-mode", "hooks", "plugins", "marketplace",
		"skills", "share", "session-info", "rename", "dashboard", "cd", "add-dir",
		"remove-dir", "theme", "feedback", "announcements", "remember", "plan", "swarm",
		"view-plan", "resume", "mcps", "workflows", "btw", "recap", "doctor", "voice",
		"loop", "imagine", "imagine-video", "timestamps", "timeline", "toggle-mouse-reporting",
		"settings", "privacy", "rewind", "jump", "login", "logout", "import-claude",
		"usage", "cache", "queue", "tasks", "release-notes", "tutorial", "config-agents",
		"personas", "gboom", "scroll-debug", "debug",
		"goal", "memory", "flush", "dream", "deep-research", "ultracode", "workflow",
	}
	for _, name := range required {
		if _, ok := ResolveSlash(name); !ok {
			t.Errorf("missing /%s", name)
		}
	}
}

func TestSlashAliasesResolveToCanonical(t *testing.T) {
	cases := map[string]string{
		"exit": "quit", "welcome": "home", "clear": "new", "ml": "multiline",
		"changelog": "release-notes", "config": "settings", "preferences": "settings",
		"prefs": "settings", "log": "transcript", "t": "theme", "show-plan": "view-plan",
		"plan-view": "view-plan", "summarize": "recap", "title": "rename", "full": "fullscreen",
		"m": "model", "cost": "usage", "undo": "rewind", "tour": "tutorial",
		"onboarding": "tutorial", "terminal-setup": "doctor", "agents": "config-agents",
		"cache-status": "cache", "howto": "docs", "sessions": "dashboard",
		"agents-dashboard": "dashboard", "yolo": "always-approve", "status": "session-info",
		"info": "session-info", "mcp": "mcps", "mem": "memory",
	}
	for alias, want := range cases {
		got, ok := ResolveSlash(alias)
		if !ok || got.Name != want {
			t.Errorf("/%s -> %q want %s", alias, got.Name, want)
		}
	}
}

func TestFuzzyPaletteRanksPrefixFirst(t *testing.T) {
	hits := FuzzySlash("/mod", modeFull)
	if len(hits) == 0 || hits[0].Name != "model" {
		t.Fatalf("expected /model first, got %+v", hits)
	}
}

func TestWelcomeListsSessionsAndLogin(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	m, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}})
	if err != nil {
		t.Fatal(err)
	}
	if m.screen != screenWelcome {
		t.Fatalf("screen %d", m.screen)
	}
	if m.unsignedIn() {
		foundCodex, foundXAI := false, false
		for _, it := range m.items {
			if it.Key == "codex" {
				foundCodex = true
			}
			if it.Key == "xai" {
				foundXAI = true
			}
		}
		if !foundCodex || !foundXAI {
			t.Fatalf("unsigned welcome must offer Codex/xAI chooser, got %+v", m.items)
		}
		return
	}
	foundLogin := false
	for _, it := range m.items {
		if it.Key == "login" {
			foundLogin = true
		}
	}
	if !foundLogin {
		t.Fatal("welcome must offer Login")
	}
}

func TestLoginSavesKeyUnderOpenGrokHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENTROPY_HOME", home)
	config.ResetHomeForTest()
	m, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}})
	if err != nil {
		t.Fatal(err)
	}
	m.openLogin("openrouter")
	m.keyBuf = "sk-or-test-not-a-real-key"
	m.handleLoginKey(tea.KeyMsg{Type: tea.KeyEnter})
	cred, ok := auth.Lookup(types.ProviderOpenRouter, false)
	if !ok || cred.Key == "" {
		t.Fatal("expected stored OpenRouter key")
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "auth.json"))
	if strings.Contains(string(raw), filepath.Join("home", ".grok")) {
		t.Fatal("must not mention ~/.grok")
	}
	var dump map[string]any
	if err := json.Unmarshal(raw, &dump); err != nil {
		t.Fatal(err)
	}
	if _, ok := dump["openrouter::api_key"]; !ok {
		t.Fatalf("scope missing: %s", raw)
	}
}

func TestThemeResolverKnowsRustNames(t *testing.T) {
	for _, name := range []string{"groknight", "dark", "grokday", "light", "tokyonight", "tokyo", "rosepine", "oscura", "auto"} {
		th := resolveTheme(name)
		if th.Cyan == "" {
			t.Errorf("theme %s", name)
		}
	}
}

func TestPermissionReplyAlwaysAllow(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	m, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}, Resume: ""})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}, Continue: false})
	_ = sess
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan bool, 1)
	m.perm = &permAskMsg{Tool: "bash", Access: "bash", Reply: ch}
	// attach a session so Always can record the rule
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.replyPerm(true, true)
	if !<-ch {
		t.Fatal("expected allow")
	}
	found := false
	for _, r := range m.sess.Allow {
		if r == "bash" {
			found = true
		}
	}
	if !found {
		t.Fatal("always-allow must append the tool")
	}
}
