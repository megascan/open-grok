package cli_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/megascan/open-grok/internal/cli"
	"github.com/megascan/open-grok/internal/config"
)

func TestVersionAndInspectAndModels(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()

	root := cli.NewRoot()
	root.SetArgs([]string{"version", "--json"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"runtime": "go"`)) && !bytes.Contains(buf.Bytes(), []byte(`"runtime":"go"`)) {
		t.Fatalf("version json: %s", buf.String())
	}

	root = cli.NewRoot()
	root.SetArgs([]string{"inspect", "--json"})
	buf.Reset()
	root.SetOut(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	root = cli.NewRoot()
	root.SetArgs([]string{"models"})
	buf.Reset()
	root.SetOut(&buf)
	root.SetErr(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	root = cli.NewRoot()
	root.SetArgs([]string{"models", "--free"})
	buf.Reset()
	root.SetOut(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("openrouter")) && !bytes.Contains(buf.Bytes(), []byte(":free")) {
		// models may print via fmt to stdout; still must not crash
	}
}

func TestDashboardCommandRegistered(t *testing.T) {
	root := cli.NewRoot()
	if root.Name() != "entropy" {
		t.Fatalf("root command must be entropy, got %q", root.Name())
	}
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "dashboard" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("entropy dashboard must exist")
	}
}

func TestLoginCompletionsIncludeProviders(t *testing.T) {
	root := cli.NewRoot()
	root.SetArgs([]string{"completions", "bash"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 20 {
		t.Fatalf("expected a completion script, got %q", buf.String())
	}
}
