package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/megascan/open-grok/internal/hooks"
)

func TestPreToolUseFailsOpenWhenMissing(t *testing.T) {
	deny, reason := hooks.PreToolUse(context.Background(), t.TempDir(), t.TempDir(), "bash", `{}`)
	if deny {
		t.Fatalf("missing hooks must fail open, got deny %q", reason)
	}
}

func TestPreToolUseExit2Denies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".entropy", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "PreToolUse")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho blocked\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	deny, reason := hooks.PreToolUse(context.Background(), cwd, cwd, "bash", `{}`)
	if !deny {
		t.Fatal("exit 2 must deny")
	}
	if reason == "" {
		t.Fatal("deny reason")
	}
}

func TestPreToolUseCrashFailsOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".entropy", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "PreToolUse")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	deny, _ := hooks.PreToolUse(context.Background(), cwd, cwd, "write_file", `{}`)
	if deny {
		t.Fatal("non-2 failures must fail open")
	}
}

func TestImportedJSONHookDenies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".entropy", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"exit 2"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "imported-from-claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	deny, reason := hooks.PreToolUse(context.Background(), cwd, cwd, "bash", `{}`)
	if !deny {
		t.Fatal("imported-from-claude.json exit 2 must deny")
	}
	if reason == "" {
		t.Fatal("deny reason")
	}
}

func TestPreToolUseReadsLegacyOpenGrokDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".opengrok", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "PreToolUse")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho blocked\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	deny, _ := hooks.PreToolUse(context.Background(), cwd, cwd, "bash", `{}`)
	if !deny {
		t.Fatal(".opengrok/hooks remains a compatibility fallback")
	}
}
