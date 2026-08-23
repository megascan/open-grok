package foreign_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/foreign"
)

func TestScanRecentFindsClaudeJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/tmp/demo-project"
	dir := filepath.Join(home, ".claude", "projects")
	// sanitizePath maps non-alnum to '-'
	sanitized := ""
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sanitized += string(r)
		} else {
			sanitized += "-"
		}
	}
	proj := filepath.Join(dir, sanitized)
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"cwd":"/tmp/demo-project","message":{"role":"user","content":"fix the tests"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "sess-1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := foreign.ScanRecent(cwd)
	if len(got) == 0 {
		t.Fatal("expected Claude session")
	}
	if got[0].Tool != "claude" {
		t.Fatalf("tool %s", got[0].Tool)
	}
	if got[0].Title != "fix the tests" {
		t.Fatalf("title %q", got[0].Title)
	}
}
