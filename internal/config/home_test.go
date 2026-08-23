package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/config"
)

func TestDefaultHomeIsOpengrokNotGrok(t *testing.T) {
	home := config.DefaultHome()
	if !strings.HasSuffix(home, ".opengrok") {
		t.Fatalf("default home %q must end with .opengrok", home)
	}
	if config.NeverGrokHome(home) {
		t.Fatal("default home must not be ~/.grok")
	}
	if strings.Contains(home, string(filepath.Separator)+".grok") && !strings.Contains(home, ".opengrok") {
		t.Fatal("must never fall back to ~/.grok")
	}
}

func TestHomeHonorsOPENGROK_HOME(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENGROK_HOME", dir)
	config.ResetHomeForTest()
	got := config.Home()
	if got != dir && !strings.HasPrefix(got, dir) {
		t.Fatalf("Home() = %q, want %q", got, dir)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(got)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("home perms %v should be owner-only", info.Mode().Perm())
		}
	}
}

func TestApplicationNameIsPlatformNative(t *testing.T) {
	dir := t.TempDir()
	app := config.ApplicationIn(dir)
	if runtime.GOOS == "windows" {
		if filepath.Base(app) != "open-grok.exe" {
			t.Fatalf("windows binary must be open-grok.exe, got %s", app)
		}
	} else if filepath.Base(app) != "open-grok" {
		t.Fatalf("unix binary must be open-grok, got %s", app)
	}
}

func TestEncodeCWDShortRoundTrip(t *testing.T) {
	cwd := "/Users/foo/project"
	encoded := config.EncodeCWDDirname(cwd)
	dir := filepath.Join(t.TempDir(), encoded)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, ok := config.DecodeCWDFromDirname(dir)
	if !ok || got != cwd {
		t.Fatalf("roundtrip %q -> %q ok=%v", cwd, got, ok)
	}
}

func TestEncodeCWDLongUsesHash(t *testing.T) {
	long := "/Users/test/" + strings.Repeat("中", 30)
	encoded := config.EncodeCWDDirname(long)
	if len(encoded) > 255 {
		t.Fatalf("encoded name too long: %d", len(encoded))
	}
	if strings.HasPrefix(encoded, "%2F") || strings.HasPrefix(encoded, "%2f") {
		t.Fatalf("long cwd should not stay url-encoded: %s", encoded)
	}
}

func TestEnsureSessionsWritesCWDFileForLongPaths(t *testing.T) {
	home := t.TempDir()
	long := "/Users/test/" + strings.Repeat("中", 30)
	dir, err := config.EnsureSessionsCWDDirIn(home, long)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := config.DecodeCWDFromDirname(dir)
	if !ok || got != long {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestNeverGrokHome(t *testing.T) {
	if !config.NeverGrokHome("/Users/me/.grok") {
		t.Fatal("expected ~/.grok to be flagged")
	}
	if config.NeverGrokHome("/Users/me/.opengrok") {
		t.Fatal(".opengrok is the allowed home")
	}
}
