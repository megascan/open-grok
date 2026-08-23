package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/workspace"
)

func TestDecidePromptWhenInteractiveAndConfigs(t *testing.T) {
	got := workspace.Decide(true, workspace.DecideInputs{
		RepoConfigsPresent: true,
		Interactive:        true,
		KeyRecordable:      true,
	})
	if got != workspace.TrustPrompt {
		t.Fatalf("got %v", got)
	}
}

func TestDecideTrustedWhenFeatureOff(t *testing.T) {
	got := workspace.Decide(false, workspace.DecideInputs{
		RepoConfigsPresent: true,
		Interactive:        true,
		KeyRecordable:      true,
	})
	if got != workspace.TrustTrusted {
		t.Fatalf("got %v", got)
	}
}

func TestDecideTrustedWhenUnsafeRoot(t *testing.T) {
	got := workspace.Decide(true, workspace.DecideInputs{
		RepoConfigsPresent: true,
		Interactive:        true,
		KeyRecordable:      false,
	})
	if got != workspace.TrustTrusted {
		t.Fatalf("got %v", got)
	}
}

func TestRepoConfigsPresentDetectsMcpJSON(t *testing.T) {
	dir := t.TempDir()
	if workspace.RepoConfigsPresent(dir) {
		t.Fatal("empty dir")
	}
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspace.RepoConfigsPresent(dir) {
		t.Fatal("expected .mcp.json to gate")
	}
}

func TestGrantFolderTrustPersistsUnderEntropyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENTROPY_HOME", home)
	config.ResetHomeForTest()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspace.ShouldPrompt(cwd) {
		t.Fatal("should prompt before grant")
	}
	if err := workspace.GrantFolderTrust(cwd); err != nil {
		t.Fatal(err)
	}
	if workspace.ShouldPrompt(cwd) {
		t.Fatal("grant must persist")
	}
	raw, err := os.ReadFile(filepath.Join(home, "trusted_folders.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("empty store")
	}
}

func TestNeverWritesGrokTrustStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENTROPY_HOME", home)
	config.ResetHomeForTest()
	cwd := t.TempDir()
	_ = workspace.GrantFolderTrust(cwd)
	if _, err := os.Stat(filepath.Join(home, ".grok")); err == nil {
		t.Fatal("must not create ~/.grok")
	}
}

func TestSanitizeWorktreeLabel(t *testing.T) {
	if got := workspace.SanitizeWorktreeLabel(" feat / bad "); got != "feat-bad" {
		t.Fatalf("%q", got)
	}
}
