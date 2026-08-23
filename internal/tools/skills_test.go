package tools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/tools"
)

func TestDiscoverSkillsSkipsVendorDefaults(t *testing.T) {
	cwd := t.TempDir()
	user := filepath.Join(cwd, ".opengrok", "skills", "mine")
	_ = os.MkdirAll(user, 0o755)
	_ = os.WriteFile(filepath.Join(user, "SKILL.md"), []byte("---\nname: mine\ndescription: user skill\n---\nbody\n"), 0o644)

	vend := filepath.Join(cwd, ".cursor", "skills", "shell")
	_ = os.MkdirAll(vend, 0o755)
	_ = os.WriteFile(filepath.Join(vend, "SKILL.md"), []byte("---\nname: shell\ndescription: vendor\n---\n"), 0o644)

	found := tools.DiscoverSkills(cwd)
	names := map[string]bool{}
	for _, s := range found {
		names[s.Name] = true
	}
	if !names["mine"] {
		t.Fatal("user skill missing")
	}
	if names["shell"] {
		t.Fatal("cursor vendor default skill must be dropped")
	}
}
