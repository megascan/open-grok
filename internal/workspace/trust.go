package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/megascan/open-grok/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

// TrustOutcome is the pure folder-trust decision (Rust folder_trust::TrustOutcome).
type TrustOutcome int

const (
	TrustTrusted TrustOutcome = iota
	TrustUntrusted
	TrustPrompt
)

type trustFile struct {
	Folders map[string]bool `toml:"folders"`
}

// FeatureEnabled is on for the shipping Entropy binary unless the user opts out.
// Env ENTROPY_FOLDER_TRUST / GROK_FOLDER_TRUST wins (0/false/off disables).
func FeatureEnabled() bool {
	if config.EnvIsOff("ENTROPY_FOLDER_TRUST", "GROK_FOLDER_TRUST") {
		return false
	}
	if config.EnvIsOn("ENTROPY_FOLDER_TRUST", "GROK_FOLDER_TRUST") {
		return true
	}
	return true
}

// ShouldPrompt is true when the TUI must show "Do you trust this directory?".
func ShouldPrompt(cwd string) bool {
	return Decide(FeatureEnabled(), DecideInputsFor(cwd)) == TrustPrompt
}

// DecideInputsFor gathers store + markers + recordability for cwd.
func DecideInputsFor(cwd string) DecideInputs {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	return DecideInputs{
		StoreTrusted:       IsTrusted(abs),
		RepoConfigsPresent: RepoConfigsPresent(abs),
		Interactive:        true,
		KeyRecordable:      !IsUnsafeTrustRoot(abs),
	}
}

// DecideInputs is the pure decide() payload.
type DecideInputs struct {
	StoreTrusted       bool
	RepoConfigsPresent bool
	Interactive        bool
	KeyRecordable      bool
}

// Decide is the Rust folder_trust::decide precedence, with no I/O.
func Decide(featureEnabled bool, i DecideInputs) TrustOutcome {
	if !featureEnabled {
		return TrustTrusted
	}
	if i.StoreTrusted {
		return TrustTrusted
	}
	if !i.KeyRecordable {
		return TrustTrusted
	}
	if !i.RepoConfigsPresent {
		return TrustTrusted
	}
	if i.Interactive {
		return TrustPrompt
	}
	return TrustUntrusted
}

// IsUnsafeTrustRoot is home, filesystem root, or a non-absolute path.
func IsUnsafeTrustRoot(key string) bool {
	if !filepath.IsAbs(key) {
		return true
	}
	clean := filepath.Clean(key)
	if clean == string(filepath.Separator) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.Clean(home) == clean {
		return true
	}
	return false
}

func storePath() string {
	return filepath.Join(config.Home(), "trusted_folders.toml")
}

func loadStore() trustFile {
	out := trustFile{Folders: map[string]bool{}}
	data, err := os.ReadFile(storePath())
	if err != nil {
		return out
	}
	_ = toml.Unmarshal(data, &out)
	if out.Folders == nil {
		out.Folders = map[string]bool{}
	}
	return out
}

func saveStore(f trustFile) error {
	if err := os.MkdirAll(filepath.Dir(storePath()), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(storePath(), data, 0o600)
}

// IsTrusted reports a recorded grant for key or an ancestor.
func IsTrusted(key string) bool {
	f := loadStore()
	cur := filepath.Clean(key)
	for {
		if f.Folders[cur] {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// GrantFolderTrust persists a yes on the welcome trust screen.
func GrantFolderTrust(cwd string) error {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if IsUnsafeTrustRoot(abs) {
		return nil
	}
	f := loadStore()
	f.Folders[filepath.Clean(abs)] = true
	return saveStore(f)
}

// RepoConfigsPresent is true when cwd→git-root contains trust-sensitive markers.
func RepoConfigsPresent(cwd string) bool {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	home, _ := os.UserHomeDir()
	home = filepath.Clean(home)
	for {
		if hasTrustMarkers(dir) {
			return true
		}
		if isGitRoot(dir) {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		if home != "" && filepath.Clean(parent) == home {
			return hasTrustMarkers(dir) // already checked; stop before scanning $HOME
		}
		dir = parent
	}
}

func isGitRoot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (st.IsDir() || st.Mode().IsRegular())
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func hasTrustMarkers(dir string) bool {
	for _, n := range []string{".mcp.json", ".envrc", "AGENTS.md", "CLAUDE.md", "Claude.md"} {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	if exists(filepath.Join(dir, ".cursor", "mcp.json")) || exists(filepath.Join(dir, ".cursor", "hooks.json")) {
		return true
	}
	if exists(filepath.Join(dir, ".claude")) {
		return true
	}
	for _, proj := range []string{config.ProjectDirName(), ".opengrok"} {
		p := filepath.Join(dir, proj)
		for _, sub := range []string{
			"hooks", "agents", "plugins", "workflows", "roles", "personas",
			"mcp.json", "lsp.json", "config.toml",
		} {
			if exists(filepath.Join(p, sub)) {
				return true
			}
		}
	}
	return false
}

// HasGitAncestor is true when cwd or a parent contains .git (worktree row).
func HasGitAncestor(cwd string) bool {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		if isGitRoot(dir) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// GitRoot walks to the nearest .git and returns that directory.
func GitRoot(cwd string) (string, bool) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		if isGitRoot(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// SanitizeWorktreeLabel keeps a branch/dir-safe subset of the dialog text.
func SanitizeWorktreeLabel(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ', r == '/':
			if b.Len() == 0 || !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		if b.Len() >= 80 {
			break
		}
	}
	return strings.Trim(b.String(), ".-")
}
