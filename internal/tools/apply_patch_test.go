package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/tools"
)

func TestApplyPatchAddUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	tc := tools.Context{CWD: dir, SessionDir: filepath.Join(dir, "sess")}

	add := "*** Begin Patch\n*** Add File: a.txt\n+hello\n+world\n*** End Patch\n"
	res := tools.ApplyPatch{}.Execute(context.Background(), tc, mustJSON(map[string]string{"patch": add}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "hello\nworld\n" {
		t.Fatalf("add got %q", got)
	}

	upd := "*** Begin Patch\n*** Update File: a.txt\n@@\n-hello\n+HELLO\n world\n*** End Patch\n"
	res = tools.ApplyPatch{}.Execute(context.Background(), tc, mustJSON(map[string]string{"patch": upd}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "HELLO\nworld" && string(got) != "HELLO\nworld\n" {
		t.Fatalf("update got %q", got)
	}

	del := "*** Begin Patch\n*** Delete File: a.txt\n*** End Patch\n"
	res = tools.ApplyPatch{}.Execute(context.Background(), tc, mustJSON(map[string]string{"patch": del}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
	if !tools.AgentWrote(filepath.Join(dir, "a.txt")) {
		t.Fatal("apply_patch must record agent writes")
	}
}

func TestApplyPatchPlanModeGateUsesEditAccess(t *testing.T) {
	var p tools.ApplyPatch
	if p.Access() != "edit" {
		t.Fatal("apply_patch is an edit tool")
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
