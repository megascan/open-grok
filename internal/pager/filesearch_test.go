package pager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func TestParseAtTokenRequiresBoundary(t *testing.T) {
	if _, ok := parseAtToken("email@x.com"); ok {
		t.Fatal("email is not an @-token")
	}
	tok, ok := parseAtToken("see @src/app")
	if !ok || tok.Query != "src/app" {
		t.Fatalf("%+v %v", tok, ok)
	}
	tok, ok = parseAtToken("see @!hidden")
	if !ok || !tok.Hidden || tok.Query != "hidden" {
		t.Fatalf("hidden %+v", tok)
	}
	if _, ok := parseAtToken("see @done more"); ok {
		t.Fatal("closed token")
	}
}

func TestAcceptAtPathFileAddsSpace(t *testing.T) {
	tok, _ := parseAtToken("read @fo")
	got := acceptAtPath("read @fo", tok, "foo.go", false, true)
	if got != "read @foo.go " {
		t.Fatalf("%q", got)
	}
}

func TestAcceptAtPathDirTabAddsSlash(t *testing.T) {
	tok, _ := parseAtToken("read @in")
	got := acceptAtPath("read @in", tok, "internal", true, true)
	if !strings.HasPrefix(got, "read @internal/") {
		t.Fatalf("%q", got)
	}
}

func TestListWorkspaceFilesFindsGoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "beta.go"), []byte("package b"), 0o644)
	hits := listWorkspaceFiles(dir, "alpha", false)
	if len(hits) == 0 || hits[0].Key != "alpha.go" {
		t.Fatalf("%+v", hits)
	}
}

func TestDashboardOpensAsScreen(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	m, err := newModel(Options{
		CWD: t.TempDir(), Dashboard: true,
		Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.screen != screenDashboard {
		t.Fatalf("screen %d", m.screen)
	}
}

func TestSettingsListsCodeModeAndRecap(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	m, err := newModel(Options{CWD: t.TempDir(), Model: catalog.Model{ID: "m", Name: "m", Provider: types.ProviderOpenRouter}})
	if err != nil {
		t.Fatal(err)
	}
	m.openSettings()
	keys := map[string]bool{}
	for _, it := range m.items {
		keys[it.Key] = true
	}
	for _, k := range []string{
		"code_mode", "follow_up_behavior", "image_generation_provider",
		"recap_model", "memory_model", "remember_tool_approvals", "coding_data_sharing",
		"render_mermaid", "page_flip_on_send", "permission_mode", "plan_mode",
	} {
		if !keys[k] {
			t.Errorf("missing setting %s", k)
		}
	}
}

func TestAcceptAtPathDirRightKeepsAtContext(t *testing.T) {
	tok, _ := parseAtToken("read @in")
	got := acceptAtPath("read @in", tok, "internal", true, false)
	if got != "read @internal" {
		t.Fatalf("%q", got)
	}
}

func TestOptionIndexFromKeyMatchesRustCard(t *testing.T) {
	if optionIndexFromKey("1", 3) != 0 {
		t.Fatal("1")
	}
	if optionIndexFromKey("a", 12) != 9 {
		t.Fatal("a is 10th option")
	}
	if optionIndexFromKey("f", 9) != -1 {
		t.Fatal("f out of range")
	}
}

func TestRustSettingsCatalogCoversRegistry(t *testing.T) {
	if len(rustSettingsCatalog()) < 80 {
		t.Fatalf("catalog too small: %d", len(rustSettingsCatalog()))
	}
}

func TestCancelTurnChoicesMatchRust(t *testing.T) {
	want := []string{"Stop running", "Continue to run", "Always stop", "Always continue"}
	if len(cancelTurnChoices) != 4 {
		t.Fatal(cancelTurnChoices)
	}
	for i, w := range want {
		if cancelTurnChoices[i] != w {
			t.Fatalf("%q", cancelTurnChoices[i])
		}
	}
}
