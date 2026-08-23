package claudeimport_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/claudeimport"
	"github.com/megascan/open-grok/internal/config"
)

func TestScanPermissionsEnvAndHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{
  "permissions": {"allow": ["Bash(*)", "Read(*)"], "deny": ["Bash(rm *)"]},
  "env": {"SECRET_TOKEN": "abcdef"},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo x", "timeout": 5}]}
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	plan := claudeimport.Scan(cwd)
	kinds := map[claudeimport.Kind]int{}
	for _, it := range plan.Items {
		kinds[it.Kind]++
		if it.Kind == claudeimport.KindEnv && it.Value != "abcdef" {
			t.Fatalf("env leaked into label or dropped: %+v", it)
		}
		if it.Kind == claudeimport.KindEnv && it.Label == "abcdef" {
			t.Fatal("env value must be redacted in the label")
		}
	}
	if kinds[claudeimport.KindPermission] != 3 {
		t.Fatalf("permissions: %d items %#v", kinds[claudeimport.KindPermission], plan.Items)
	}
	if kinds[claudeimport.KindEnv] != 1 {
		t.Fatalf("env: %d", kinds[claudeimport.KindEnv])
	}
	if kinds[claudeimport.KindHook] != 1 {
		t.Fatalf("hooks: %d", kinds[claudeimport.KindHook])
	}
}

func TestApplyWritesEntropyConfigAndMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENTROPY_HOME", home)
	config.ResetHomeForTest()
	items := []claudeimport.Item{
		{Scope: claudeimport.ScopeGlobal, Kind: claudeimport.KindPermission, Action: "allow", Rule: "Bash(*)"},
		{Scope: claudeimport.ScopeGlobal, Kind: claudeimport.KindMCP, MCPName: "fs", MCP: config.MCP{Command: "npx"}},
	}
	res, err := claudeimport.Apply(t.TempDir(), items)
	if err != nil {
		t.Fatal(err)
	}
	if res.GlobalCount < 2 {
		t.Fatalf("count %+v", res)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ClaudeCompat.Imported {
		t.Fatal("must set [claude_compat] imported")
	}
	if len(cfg.Permission.Allow) == 0 || cfg.MCP["fs"].Command != "npx" {
		t.Fatalf("config not merged: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".grok")); err == nil {
		t.Fatal("must not write ~/.grok")
	}
}

func TestHasPendingImportRespectsMarker(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if claudeimport.HasPendingImport() {
		t.Fatal("no ~/.claude")
	}
	if err := os.MkdirAll(filepath.Join(user, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !claudeimport.HasPendingImport() {
		t.Fatal("expected pending")
	}
	_, err := claudeimport.Apply(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if claudeimport.HasPendingImport() {
		t.Fatal("imported marker must hide the welcome row")
	}
}
