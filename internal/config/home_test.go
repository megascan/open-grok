package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/config"
)

func TestDefaultHomeIsEntropyNotGrok(t *testing.T) {
	home := config.DefaultHome()
	if !strings.HasSuffix(home, ".entropy") {
		t.Fatalf("default home %q must end with .entropy", home)
	}
	if config.NeverGrokHome(home) {
		t.Fatal("default home must not be ~/.grok")
	}
	if strings.Contains(home, string(filepath.Separator)+".grok"+string(filepath.Separator)) {
		t.Fatal("must never fall back to ~/.grok")
	}
	if strings.HasSuffix(home, ".opengrok") {
		t.Fatal("Entropy must not use ~/.opengrok as the default home")
	}
}

func TestHomeHonorsENTROPY_HOME(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENTROPY_HOME", dir)
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
		if filepath.Base(app) != "entropy.exe" {
			t.Fatalf("windows binary must be entropy.exe, got %s", app)
		}
	} else if filepath.Base(app) != "entropy" {
		t.Fatalf("unix binary must be entropy, got %s", app)
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
	if config.NeverGrokHome("/Users/me/.entropy") {
		t.Fatal(".entropy is the allowed home")
	}
	if config.NeverGrokHome("/Users/me/.opengrok") {
		t.Fatal(".opengrok is a foreign product home, not ~/.grok")
	}
}

func TestPrefBoolDefaultsOnWhenUnset(t *testing.T) {
	var f config.File
	if !f.PrefBool("page_flip_on_send", f.UI.PageFlipOnSend, true) {
		t.Fatal("missing pref must default on (Rust page_flip_on_send)")
	}
	if !f.PrefBool("group_tool_verbs", f.UI.GroupToolVerbs, true) {
		t.Fatal("missing pref must default on (Rust group_tool_verbs)")
	}
	if !f.PrefBool("simple_mode", f.UI.SimpleMode, true) {
		t.Fatal("missing pref must default on (Rust simple_mode)")
	}
	f.SetPrefBool("page_flip_on_send", false)
	if f.PrefBool("page_flip_on_send", false, true) {
		t.Fatal("explicit false must win")
	}
}

func TestHomeFallsBackToOPENGROK_HOME(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENTROPY_HOME", "")
	t.Setenv("OPENGROK_HOME", dir)
	config.ResetHomeForTest()
	got := config.Home()
	if got != dir && !strings.HasPrefix(got, dir) {
		t.Fatalf("Home() = %q, want OPENGROK_HOME fallback %q", got, dir)
	}
}
