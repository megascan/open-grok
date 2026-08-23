package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestSearchReplaceAndHunkAttribution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := tools.Context{CWD: dir, SessionDir: filepath.Join(dir, "sess")}
	args, _ := json.Marshal(map[string]string{
		"path":       path,
		"old_string": "world",
		"new_string": "grok",
	})
	res := tools.SearchReplace{}.Execute(context.Background(), tc, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello grok" {
		t.Fatalf("got %q", got)
	}
	if !tools.AgentWrote(path) {
		t.Fatal("agent writes must be recorded; fsnotify alone would mark them External")
	}
}

func TestGrepFindsLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"pattern": "Hello", "glob": "*.go"})
	res := tools.Grep{}.Execute(context.Background(), tools.Context{CWD: dir}, args)
	if res.IsError || res.Content == "no matches" {
		t.Fatalf("grep failed: %s", res.Content)
	}
}

func TestBashEcho(t *testing.T) {
	dir := t.TempDir()
	args, _ := json.Marshal(map[string]string{"command": "echo open-grok"})
	res := tools.Bash{}.Execute(context.Background(), tools.Context{CWD: dir}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if got := res.Content; got != "open-grok\n" && got != "open-grok\r\n" && got != "open-grok" {
		t.Fatalf("bash output %q", got)
	}
}

func TestDefaultPackAccessKinds(t *testing.T) {
	want := map[string]types.AccessKind{
		"bash":           types.AccessBash,
		"write_file":     types.AccessEdit,
		"search_replace": types.AccessEdit,
		"read_file":      types.AccessRead,
	}
	for _, ttool := range tools.DefaultPack() {
		if k, ok := want[ttool.Name()]; ok && ttool.Access() != k {
			t.Errorf("%s access %s want %s", ttool.Name(), ttool.Access(), k)
		}
	}
}
