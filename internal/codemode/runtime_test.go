package codemode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/codemode"
	"github.com/megascan/open-grok/internal/tools"
)

func TestPersistentExecAndNestedWrite(t *testing.T) {
	dir := t.TempDir()
	host := tools.Context{CWD: dir, SessionDir: filepath.Join(dir, "s")}
	rt := codemode.New(host, tools.NestedPack())
	_, err := rt.Exec(context.Background(), `n = 1`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := rt.Exec(context.Background(), `n = n + 1; n`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "2" {
		t.Fatalf("persistent vm lost state: %q", out)
	}
	_, err = rt.Exec(context.Background(), `tools.write_file({path:"x.txt", contents:"from-js"})`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "from-js" {
		t.Fatalf("got %q", got)
	}
	if !tools.AgentWrote(filepath.Join(dir, "x.txt")) {
		t.Fatal("nested tools.* writes must record agent hunks")
	}
}
