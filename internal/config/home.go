package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	homeEnvName          = "ENTROPY_HOME"
	legacyHomeEnvName    = "OPENGROK_HOME"
	defaultHomeName      = ".entropy"
	projectDirName       = ".entropy"
	legacyProjectDirName = ".opengrok"
	binaryName           = "entropy"
	windowsBinary        = "entropy.exe"
	maxDirnameBytes      = 255
)

var (
	homeOnce sync.Once
	cached   string
)

// HomeEnv is the environment variable that relocates user state.
func HomeEnv() string { return homeEnvName }

// BinaryName is the public command (`entropy` / `entropy.exe`).
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return windowsBinary
	}
	return binaryName
}

// ProjectDirName is the per-repo config directory (`.entropy`).
func ProjectDirName() string { return projectDirName }

// ProjectDir is cwd/.entropy.
func ProjectDir(cwd string) string {
	return filepath.Join(cwd, projectDirName)
}

// ProjectSubdir returns cwd/.entropy/<name> then cwd/.opengrok/<name>
// so existing project trees keep working after the rename.
func ProjectSubdir(cwd, name string) []string {
	return []string{
		filepath.Join(cwd, projectDirName, name),
		filepath.Join(cwd, legacyProjectDirName, name),
	}
}

// FirstEnv returns the first non-empty environment value among names.
func FirstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// EnvIsOn is true when the first set variable is 1/true/yes/on.
func EnvIsOn(names ...string) bool {
	switch strings.ToLower(FirstEnv(names...)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// EnvIsOff is true when the first set variable is 0/false/no/off.
func EnvIsOff(names ...string) bool {
	switch strings.ToLower(FirstEnv(names...)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// Home returns the per-user Entropy directory: $ENTROPY_HOME or ~/.entropy.
// The directory is created if it does not exist.
func Home() string {
	homeOnce.Do(func() {
		cached = resolveHome()
		_ = os.MkdirAll(cached, 0o700)
		setOwnerOnly(cached)
	})
	return cached
}

// ResetHomeForTest clears the cached home. Tests should set ENTROPY_HOME first.
func ResetHomeForTest() {
	homeOnce = sync.Once{}
	cached = ""
}

func resolveHome() string {
	if v := FirstEnv(homeEnvName, legacyHomeEnvName); v != "" {
		return filepath.Clean(v)
	}
	return DefaultHome()
}

// DefaultHome is ~/.entropy without creating it.
func DefaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(filepath.Clean(home), defaultHomeName)
}

// UserHome returns the user-global Entropy home only when one genuinely
// resolves, so callers that scan hooks/marketplace do not mistake a project
// .entropy tree for the user-global one.
func UserHome() (string, bool) {
	if FirstEnv(homeEnvName, legacyHomeEnvName) != "" {
		return Home(), true
	}
	if _, err := os.UserHomeDir(); err == nil {
		return Home(), true
	}
	return "", false
}

// Application is $ENTROPY_HOME/bin/entropy (.exe on Windows).
func Application() string {
	return ApplicationIn(Home())
}

// ApplicationIn returns the managed binary path under an explicit home.
func ApplicationIn(home string) string {
	return filepath.Join(home, "bin", BinaryName())
}

// SystemConfigDir is /etc/entropy on Unix and empty on Windows.
func SystemConfigDir() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/etc/entropy"
}

// AuthJSON is the isolated credential store for non-Codex providers.
func AuthJSON() string {
	return filepath.Join(Home(), "auth.json")
}

// CodexAuthJSON is the isolated ChatGPT Codex credential store.
func CodexAuthJSON() string {
	return filepath.Join(Home(), "codex-auth.json")
}

// ConfigTOML is ~/.entropy/config.toml.
func ConfigTOML() string {
	return filepath.Join(Home(), "config.toml")
}

// LeaderSock is the Unix-domain leader socket (unused on Windows).
func LeaderSock() string {
	return filepath.Join(Home(), "leader.sock")
}

// SessionsRoot is ~/.entropy/sessions.
func SessionsRoot() string {
	return filepath.Join(Home(), "sessions")
}

// NeverGrokHome reports whether a path is the forbidden upstream ~/.grok tree.
func NeverGrokHome(path string) bool {
	base := filepath.Base(filepath.Clean(path))
	return base == ".grok"
}
