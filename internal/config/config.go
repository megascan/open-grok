package config

import (
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// File is the user-level Entropy configuration (~/.entropy/config.toml).
type File struct {
	Model      string         `toml:"model"`
	YOLO       bool           `toml:"always_approve"`
	CLI        CLISection     `toml:"cli"`
	UI         UISection      `toml:"ui"`
	Models     ModelsSection  `toml:"models"`
	Features   FeatSection    `toml:"features"`
	Dashboard  DashSection    `toml:"dashboard"`
	SessionBus SessionBusSec  `toml:"session_bus"`
	MCP        map[string]MCP `toml:"mcp_servers"`
	Prefs      Prefs          `toml:"prefs"`
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
	CodeMode          string `toml:"code_mode"`
	FollowUp          string `toml:"follow_up_behavior"`
	ImageGen          string `toml:"image_generation_provider"`
	RecapModel        string `toml:"-"`
	MemoryModel       string `toml:"-"`
	ShowTimeline      bool   `toml:"show_timeline"`
	PageFlipOnSend    bool   `toml:"page_flip_on_send"`
	CombineQueued     bool   `toml:"combine_queued_prompts"`
	ConfirmRewind     bool   `toml:"confirm_before_rewind"`
	EnterSteers       bool   `toml:"enter_steers"`
	SimpleMode        bool   `toml:"simple_mode"`
	RenderMermaid     bool   `toml:"render_mermaid"`
	GroupToolVerbs    bool   `toml:"group_tool_verbs"`
	CollapsedEdits    bool   `toml:"collapsed_edit_blocks"`
	ShowTips          bool   `toml:"show_tips"`
	AutoUpdate        bool   `toml:"auto_update"`
	StreamToolCalls   bool   `toml:"stream_tool_calls"`
	VoiceKeybind      bool   `toml:"voice_keybind_enabled"`
	KeepSelection     bool   `toml:"keep_text_selection"`
	RespectFolds      bool   `toml:"respect_manual_folds"`
	InvertScroll      bool   `toml:"invert_scroll"`
	KimiEndpoint      string `toml:"kimi_api_endpoint"`
	ScrollMode        string `toml:"scroll_mode"`
	HunkTracker       string `toml:"hunk_tracker_mode"`
	DefaultPermChoice string `toml:"default_selected_permission"`
	ForkSecondary     string `toml:"fork_secondary_model"`
	AutoDarkTheme     string `toml:"auto_dark_theme"`
	AutoLightTheme    string `toml:"auto_light_theme"`
}

// Prefs are extra settings-modal keys persisted as strings (Rust registry
// rows that do not yet have a dedicated runtime field).
type Prefs map[string]string

// ModelsSection holds default and auxiliary model pins.
type ModelsSection struct {
	Default string `toml:"default"`
	Recap   string `toml:"recap"`
	Memory  string `toml:"memory"`
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
	if cfg.Model == "" {
		cfg.Model = cfg.Models.Default
	}
	if cfg.UI.RecapModel == "" {
		cfg.UI.RecapModel = cfg.Models.Recap
	}
	if cfg.UI.MemoryModel == "" {
		cfg.UI.MemoryModel = cfg.Models.Memory
	}
	if cfg.Prefs == nil {
		cfg.Prefs = Prefs{}
	}
	return cfg, nil
}

// DashboardEnabled reports whether the agent dashboard is on (default true).
func (f File) DashboardEnabled() bool {
	if EnvIsOff("ENTROPY_AGENT_DASHBOARD", "GROK_AGENT_DASHBOARD") {
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

// MemoryEnabled is on when configured or ENTROPY_MEMORY=1 (GROK_MEMORY is an alias).
func (f File) MemoryEnabled() bool {
	if EnvIsOn("ENTROPY_MEMORY", "GROK_MEMORY") {
		return true
	}
	return f.Features.Memory
}

// PrefBool returns a setting that stays at defaultOn until Prefs records an
// explicit true/false. Missing TOML bools are false in Go, which would invert
// Rust defaults such as page_flip_on_send and group_tool_verbs.
func (f File) PrefBool(key string, field bool, defaultOn bool) bool {
	if f.Prefs != nil {
		if v, ok := f.Prefs[key]; ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "on", "yes":
				return true
			case "false", "0", "off", "no":
				return false
			}
		}
	}
	if field {
		return true
	}
	return defaultOn
}

// SetPrefBool records an explicit boolean so later loads do not fall back to
// the Rust-matching default.
func (f *File) SetPrefBool(key string, on bool) {
	if f.Prefs == nil {
		f.Prefs = Prefs{}
	}
	if on {
		f.Prefs[key] = "true"
	} else {
		f.Prefs[key] = "false"
	}
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
