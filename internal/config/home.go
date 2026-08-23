// Package config resolves Open Grok filesystem locations and user configuration.
//
// State is isolated to $OPENGROK_HOME or ~/.opengrok. Callers must never fall
// back to ~/.grok — that path belongs to upstream Grok Build.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	homeEnvName     = "OPENGROK_HOME"
	defaultHomeName = ".opengrok"
	binaryName      = "open-grok"
	windowsBinary   = "open-grok.exe"
	maxDirnameBytes = 255
)

var (
	homeOnce sync.Once
	cached   string
)

// Home returns the per-user Open Grok directory: $OPENGROK_HOME or ~/.opengrok.
// The directory is created if it does not exist.
func Home() string {
	homeOnce.Do(func() {
		cached = resolveHome()
		_ = os.MkdirAll(cached, 0o700)
		setOwnerOnly(cached)
	})
	return cached
}

// ResetHomeForTest clears the cached home. Tests should set OPENGROK_HOME first.
func ResetHomeForTest() {
	homeOnce = sync.Once{}
	cached = ""
}

func resolveHome() string {
	if v := strings.TrimSpace(os.Getenv(homeEnvName)); v != "" {
		return filepath.Clean(v)
	}
	return DefaultHome()
}

// DefaultHome is ~/.opengrok without creating it.
func DefaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(filepath.Clean(home), defaultHomeName)
}

// UserHome returns the user-global Open Grok home only when one genuinely
// resolves, so callers that scan hooks/marketplace do not mistake a project
// .opengrok tree for the user-global one.
func UserHome() (string, bool) {
	if os.Getenv(homeEnvName) != "" {
		return Home(), true
	}
	if _, err := os.UserHomeDir(); err == nil {
		return Home(), true
	}
	return "", false
}

// Application is $OPENGROK_HOME/bin/open-grok (.exe on Windows).
func Application() string {
	return ApplicationIn(Home())
}

// ApplicationIn returns the managed binary path under an explicit home.
func ApplicationIn(home string) string {
	name := binaryName
	if runtime.GOOS == "windows" {
		name = windowsBinary
	}
	return filepath.Join(home, "bin", name)
}

// SystemConfigDir is /etc/opengrok on Unix and empty on Windows.
func SystemConfigDir() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/etc/opengrok"
}

// AuthJSON is the isolated credential store for non-Codex providers.
func AuthJSON() string {
	return filepath.Join(Home(), "auth.json")
}

// CodexAuthJSON is the isolated ChatGPT Codex credential store.
func CodexAuthJSON() string {
	return filepath.Join(Home(), "codex-auth.json")
}

// ConfigTOML is ~/.opengrok/config.toml.
func ConfigTOML() string {
	return filepath.Join(Home(), "config.toml")
}

// LeaderSock is the Unix-domain leader socket (unused on Windows).
func LeaderSock() string {
	return filepath.Join(Home(), "leader.sock")
}

// SessionsRoot is ~/.opengrok/sessions.
func SessionsRoot() string {
	return filepath.Join(Home(), "sessions")
}

// NeverGrokHome reports whether a path is the forbidden upstream ~/.grok tree.
func NeverGrokHome(path string) bool {
	base := filepath.Base(filepath.Clean(path))
	return base == ".grok"
}
