package config

import (
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// File is the user-level Open Grok configuration (~/.opengrok/config.toml).
type File struct {
	Model      string         `toml:"model"`
	YOLO       bool           `toml:"always_approve"`
	CLI        CLISection     `toml:"cli"`
	UI         UISection      `toml:"ui"`
	Features   FeatSection    `toml:"features"`
	Dashboard  DashSection    `toml:"dashboard"`
	SessionBus SessionBusSec  `toml:"session_bus"`
	MCP        map[string]MCP `toml:"mcp_servers"`
}

// UISection is the interactive pager appearance and input contract.
type UISection struct {
	Theme             string `toml:"theme"`
	VimMode           bool   `toml:"vim_mode"`
	Timestamps        bool   `toml:"timestamps"`
	CompactMode       bool   `toml:"compact_mode"`
	Multiline         bool   `toml:"multiline"`
	ScreenMode        string `toml:"screen_mode"`
	SwarmMode         bool   `toml:"swarm_mode"`
	MouseReporting    *bool  `toml:"mouse_reporting"`
	ShowThinking      *bool  `toml:"show_thinking_blocks"`
	PrivacyOptOut     bool   `toml:"privacy_opt_out"`
	RememberApprovals bool   `toml:"remember_tool_approvals"`
}

// FeatSection holds optional product features.
type FeatSection struct {
	Memory bool `toml:"memory"`
}

// CLISection controls leader attach defaults.
type CLISection struct {
	UseLeader bool `toml:"use_leader"`
}

// DashSection controls the agent dashboard.
type DashSection struct {
	Enabled *bool `toml:"enabled"`
}

// SessionBusSec opts the machine-local session bus in or out.
type SessionBusSec struct {
	Enabled *bool `toml:"enabled"`
}

// MCP is one configured MCP server (stdio or HTTP).
type MCP struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
}

// Load reads config.toml if present; missing files yield defaults.
func Load() (File, error) {
	var cfg File
	data, err := os.ReadFile(ConfigTOML())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DashboardEnabled reports whether the agent dashboard is on (default true).
func (f File) DashboardEnabled() bool {
	if os.Getenv("GROK_AGENT_DASHBOARD") == "0" {
		return false
	}
	if f.Dashboard.Enabled == nil {
		return true
	}
	return *f.Dashboard.Enabled
}

// SessionBusEnabled reports whether the machine-local session bus is on.
func (f File) SessionBusEnabled() bool {
	if f.SessionBus.Enabled == nil {
		return true
	}
	return *f.SessionBus.Enabled
}

// MouseReportingOn defaults to true (the TUI enables cell motion).
func (f File) MouseReportingOn() bool {
	if f.UI.MouseReporting == nil {
		return true
	}
	return *f.UI.MouseReporting
}

// ShowThinkingOn defaults to true.
func (f File) ShowThinkingOn() bool {
	if f.UI.ShowThinking == nil {
		return true
	}
	return *f.UI.ShowThinking
}

// MemoryEnabled is on when configured or GROK_MEMORY=1.
func (f File) MemoryEnabled() bool {
	if os.Getenv("GROK_MEMORY") == "1" || os.Getenv("GROK_MEMORY") == "true" {
		return true
	}
	return f.Features.Memory
}

// ScreenModeName is fullscreen unless the user picked minimal.
func (f File) ScreenModeName() string {
	if strings.EqualFold(f.UI.ScreenMode, "minimal") {
		return "minimal"
	}
	return "fullscreen"
}

// Save writes config.toml with owner-only permissions.
func Save(f File) error {
	if err := os.MkdirAll(filepath.Dir(ConfigTOML()), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := ConfigTOML() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ConfigTOML())
}
