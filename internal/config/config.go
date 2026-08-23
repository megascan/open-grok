package config

import (
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

// File is the user-level Open Grok configuration (~/.opengrok/config.toml).
type File struct {
	Model      string         `toml:"model"`
	YOLO       bool           `toml:"always_approve"`
	CLI        CLISection     `toml:"cli"`
	Dashboard  DashSection    `toml:"dashboard"`
	SessionBus SessionBusSec  `toml:"session_bus"`
	MCP        map[string]MCP `toml:"mcp_servers"`
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
