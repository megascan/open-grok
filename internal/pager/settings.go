package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
)

// settingDef is one Rust settings-registry row (xai-grok-pager settings/defs.rs).
type settingDef struct {
	Key, Category, Label, Kind string
	Choices                    []string
	Min, Max                   int64  // int kind (Rust SettingKind::Int)
	Default                    string // string/int seed when Prefs has no row
}

// rustSettingsCatalog is the user-visible settings modal, keyed like the Rust pager.
func rustSettingsCatalog() []settingDef {
	return []settingDef{
		{"compact_mode", "Appearance", "Compact mode", "bool", nil, 0, 0, ""},
		{"screen_mode", "Appearance", "Default screen mode", "enum", []string{"fullscreen", "minimal"}, 0, 0, ""},
		{"show_timestamps", "Appearance", "Timestamps", "bool", nil, 0, 0, ""},
		{"show_timeline", "Appearance", "Show timeline", "bool", nil, 0, 0, ""},
		{"page_flip_on_send", "Appearance", "Page flip on send", "bool", nil, 0, 0, ""},
		{"simple_mode", "Appearance", "Simple mode", "bool", nil, 0, 0, ""},
		{"vim_mode", "Appearance", "Vim-mode scrollback", "bool", nil, 0, 0, ""},
		{"theme", "Appearance", "Theme", "action", nil, 0, 0, ""},
		{"auto_dark_theme", "Appearance", "Auto dark theme", "enum", []string{"groknight", "tokyonight", "rosepine-moon", "oscura-midnight"}, 0, 0, ""},
		{"auto_light_theme", "Appearance", "Auto light theme", "enum", []string{"grokday"}, 0, 0, ""},
		{"render_mermaid", "Appearance", "Render mermaid", "bool", nil, 0, 0, ""},
		{"show_thinking_blocks", "Appearance", "Show thinking blocks", "bool", nil, 0, 0, ""},
		{"prompt_suggestions", "Appearance", "Prompt suggestions", "bool", nil, 0, 0, ""},
		{"respect_manual_folds", "Appearance", "Respect manual folds", "bool", nil, 0, 0, ""},
		{"group_tool_verbs", "Appearance", "Group tool verbs", "bool", nil, 0, 0, ""},
		{"collapsed_edit_blocks", "Appearance", "Collapsed edit blocks", "bool", nil, 0, 0, ""},
		{"display_refresh_auto_cadence", "Appearance", "Auto display-refresh cadence", "bool", nil, 0, 0, ""},
		{"scroll_speed", "Appearance", "Scroll speed", "int", nil, 1, 100, "50"},
		{"scroll_mode", "Appearance", "Scroll mode", "enum", []string{"line", "block", "page"}, 0, 0, ""},
		{"scroll_lines", "Appearance", "Scroll lines", "int", nil, 1, 10, "3"},
		{"max_thoughts_width", "Appearance", "Max thoughts width", "int", nil, 40, 500, "120"},
		{"invert_scroll", "Appearance", "Invert scroll", "bool", nil, 0, 0, ""},
		{"keep_text_selection", "Appearance", "Keep text selection", "bool", nil, 0, 0, ""},
		{"show_tips", "Appearance", "Show tips", "bool", nil, 0, 0, ""},
		{"ui.mouse_reporting_toggle", "Appearance", "Mouse reporting", "bool", nil, 0, 0, ""},
		{"combine_queued_prompts", "Editor", "Combine queued prompts", "bool", nil, 0, 0, ""},
		{"follow_up_behavior", "Editor", "Follow-up behavior", "enum", []string{"queue", "steer"}, 0, 0, ""},
		{"confirm_before_rewind", "Editor", "Confirm before rewind", "bool", nil, 0, 0, ""},
		{"enter_steers", "Editor", "Enter steers", "bool", nil, 0, 0, ""},
		{"multiline_mode", "Editor", "Multiline input", "bool", nil, 0, 0, ""},
		{"swarm_mode", "Agent", "Swarm mode", "bool", nil, 0, 0, ""},
		{"antigravity_subagents", "Agent", "Antigravity subagents", "bool", nil, 0, 0, ""},
		{"antigravity_skip_permissions", "Agent", "Antigravity skip permissions", "bool", nil, 0, 0, ""},
		{"permission_mode", "Agent", "Permission mode", "enum", []string{"default", "ask", "auto", "always-approve"}, 0, 0, ""},
		{"remember_tool_approvals", "Agent", "Remember tool approvals", "bool", nil, 0, 0, ""},
		{"code_mode", "Agent", "Code mode", "enum", []string{"direct", "code_mode", "code_mode_only"}, 0, 0, ""},
		{"plan_mode", "Agent", "Plan mode", "bool", nil, 0, 0, ""},
		{"default_selected_permission", "Agent", "Default permission selection", "enum", []string{"allow", "always", "deny"}, 0, 0, ""},
		{"stream_tool_calls", "Agent", "Stream tool calls", "bool", nil, 0, 0, ""},
		{"hunk_tracker_mode", "Agent", "Hunk tracker", "enum", []string{"off", "agent", "all"}, 0, 0, ""},
		{"sandbox.auto_allow_bash", "Agent", "Sandbox auto-allow bash", "bool", nil, 0, 0, ""},
		{"tools.respect_gitignore", "Agent", "Respect gitignore", "bool", nil, 0, 0, ""},
		{"image_generation_provider", "Models", "Image generation", "enum", []string{"grok", "openai"}, 0, 0, ""},
		{"default_model", "Models", "Default model", "action", nil, 0, 0, ""},
		{"kimi_api_endpoint", "Models", "Kimi API endpoint", "enum", []string{"platform", "code"}, 0, 0, ""},
		{"kimi_api_key", "Models", "Kimi Platform API key", "secret", nil, 0, 0, ""},
		{"kimi_code_api_key", "Models", "Kimi Code API key", "secret", nil, 0, 0, ""},
		{"fireworks_api_key", "Models", "Fireworks API key", "secret", nil, 0, 0, ""},
		{"deepseek_api_key", "Models", "DeepSeek API key", "secret", nil, 0, 0, ""},
		{"meta_api_key", "Models", "Meta API key", "secret", nil, 0, 0, ""},
		{"opencode_go_api_key", "Models", "OpenCode Go API key", "secret", nil, 0, 0, ""},
		{"wafer_api_key", "Models", "Wafer AI API key", "secret", nil, 0, 0, ""},
		{"zai_api_key", "Models", "Z AI API key", "secret", nil, 0, 0, ""},
		{"runinfra_api_key", "Models", "RunInfra API key", "secret", nil, 0, 0, ""},
		{"gemini_api_key", "Models", "Google Gemini API key", "secret", nil, 0, 0, ""},
		{"openrouter_api_key", "Models", "OpenRouter API key", "secret", nil, 0, 0, ""},
		{"opencode_go_models", "Models", "OpenCode Go live catalog", "bool", nil, 0, 0, ""},
		{"openrouter_models", "Models", "OpenRouter live catalog", "bool", nil, 0, 0, ""},
		{"custom_models", "Models", "Custom models", "action", nil, 0, 0, ""},
		{"custom_model_id", "Models", "Catalog key", "string", nil, 0, 0, ""},
		{"custom_model_slug", "Models", "Model id", "string", nil, 0, 0, ""},
		{"custom_model_name", "Models", "Display name", "string", nil, 0, 0, ""},
		{"custom_model_base_url", "Models", "Base URL", "string", nil, 0, 0, ""},
		{"custom_model_env_key", "Models", "Env key name", "string", nil, 0, 0, ""},
		{"custom_model_context_window", "Models", "Context window", "int", nil, 1000, 4000000, "128000"},
		{"recap_model", "Models", "Recap model", "action", nil, 0, 0, ""},
		{"memory_model", "Models", "Memory model", "action", nil, 0, 0, ""},
		{"fork_secondary_model", "Models", "Fork secondary model", "action", nil, 0, 0, ""},
		{"toolset.perplexity_web_search.enabled", "Models", "Perplexity web search", "bool", nil, 0, 0, ""},
		{"perplexity_api_key", "Models", "Perplexity API key", "secret", nil, 0, 0, ""},
		{"toolset.web_search_source.xai", "Models", "xAI web search source", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.codex", "Models", "Codex web search source", "enum", []string{"native", "xai", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.kimi_platform", "Models", "Kimi Platform web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.kimi_code", "Models", "Kimi Code web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.fireworks", "Models", "Fireworks web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.deepseek", "Models", "DeepSeek web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.opencode_go", "Models", "OpenCode Go web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.web_search_source.openrouter", "Models", "OpenRouter web search", "enum", []string{"native", "perplexity"}, 0, 0, ""},
		{"toolset.x_search.enabled", "Models", "X search", "bool", nil, 0, 0, ""},
		{"coding_data_sharing", "Privacy", "Coding data sharing", "enum", []string{"opt-in", "opt-out"}, 0, 0, ""},
		{"toolset.ask_user_question.timeout_enabled", "Agent", "Question-card timeout", "bool", nil, 0, 0, ""},
		{"contextual_hints", "Appearance", "Contextual hints", "bool", nil, 0, 0, ""},
		{"contextual_hints.undo", "Appearance", "Hint: undo", "bool", nil, 0, 0, ""},
		{"contextual_hints.plan_mode", "Appearance", "Hint: plan mode", "bool", nil, 0, 0, ""},
		{"contextual_hints.image_input", "Appearance", "Hint: image input", "bool", nil, 0, 0, ""},
		{"contextual_hints.send_now", "Appearance", "Hint: send now", "bool", nil, 0, 0, ""},
		{"contextual_hints.small_screen", "Appearance", "Hint: small screen", "bool", nil, 0, 0, ""},
		{"contextual_hints.word_select", "Appearance", "Hint: word select", "bool", nil, 0, 0, ""},
		{"contextual_hints.ssh_wrap", "Appearance", "Hint: SSH wrap", "bool", nil, 0, 0, ""},
		{"auto_update", "Advanced", "Auto update", "bool", nil, 0, 0, ""},
		{"voice_keybind_enabled", "Input", "Voice keybind", "bool", nil, 0, 0, ""},
		{"voice_capture_mode", "Input", "Voice capture", "enum", []string{"hold", "toggle"}, 0, 0, ""},
		{"voice_stt_language", "Input", "Voice STT language", "enum", []string{"auto", "en", "zh"}, 0, 0, ""},
		{"memory.enabled", "Memory", "Memory", "bool", nil, 0, 0, ""},
		{"memory.dream.enabled", "Memory", "Memory dream", "bool", nil, 0, 0, ""},
		{"features.telemetry", "Advanced", "Telemetry", "bool", nil, 0, 0, ""},
		{"features.lsp_tools", "Advanced", "LSP tools", "bool", nil, 0, 0, ""},
		{"features.web_fetch", "Advanced", "Web fetch", "bool", nil, 0, 0, ""},
		{"toolset.web_fetch.allow_local", "Advanced", "Web fetch allow local", "bool", nil, 0, 0, ""},
		{"features.two_pass_compaction", "Advanced", "Two-pass compaction", "bool", nil, 0, 0, ""},
		{"features.non_git_warning", "Advanced", "Non-git warning", "bool", nil, 0, 0, ""},
		{"features.remember_mode", "Advanced", "Remember mode", "bool", nil, 0, 0, ""},
		{"doom_loop_recovery.enabled", "Advanced", "Doom-loop recovery", "bool", nil, 0, 0, ""},
		{"features.subagent_worktree_snapshot", "Advanced", "Subagent worktree snapshot", "bool", nil, 0, 0, ""},
		{"diagnostics.crash_handler", "Advanced", "Crash handler", "bool", nil, 0, 0, ""},
		{"suggestions.enabled", "Appearance", "Suggestions", "bool", nil, 0, 0, ""},
		{"suggestions.ai_enabled", "Appearance", "AI suggestions", "bool", nil, 0, 0, ""},
	}
}

func (m *model) openSettings() {
	m.openSettingsFocus("")
}

func (m *model) openSettingsFocus(key string) {
	m.overlay = overlaySettings
	m.cursor = 0
	m.filter = ""
	m.settingsFilter = false
	m.settingsEnumKey = ""
	m.settingsEnumOrig = ""
	m.settingsEnumIdx = 0
	m.settingsSecretKey = ""
	m.settingsSecret = ""
	m.clearSettingsEditor()
	m.fillSettings()
	m.cursor = firstNonHeader(m.items)
	if key == "" {
		return
	}
	for i, it := range m.items {
		if it.Key == key {
			m.cursor = i
			return
		}
	}
}

func settingHeaderLabel(cat string) string {
	switch cat {
	case "Appearance":
		return "Appearance"
	case "Editor", "Input":
		return "Editor & Input"
	case "Agent":
		return "Agent & Approval"
	case "Privacy":
		return "Privacy"
	case "Models":
		return "Models"
	case "Memory":
		return "Session"
	case "Advanced":
		return "Advanced"
	default:
		return cat
	}
}

var settingsHeaderOrder = []string{
	"Appearance",
	"Editor & Input",
	"Agent & Approval",
	"Privacy",
	"Models",
	"Session",
	"Advanced",
}

func (m *model) fillSettings() {
	m.items = nil
	catalog := rustSettingsCatalog()
	grouped := map[string][]settingDef{}
	for _, d := range catalog {
		h := settingHeaderLabel(d.Category)
		grouped[h] = append(grouped[h], d)
	}
	filter := strings.ToLower(strings.TrimSpace(m.filter))
	for _, header := range settingsHeaderOrder {
		defs := grouped[header]
		var rows []listItem
		for _, d := range defs {
			it := listItem{
				Key:    d.Key,
				Label:  d.Label,
				Detail: m.settingValue(d),
			}
			if filter != "" {
				blob := strings.ToLower(d.Key + " " + d.Label + " " + d.Category + " " + it.Detail)
				if !strings.Contains(blob, filter) {
					continue
				}
			}
			rows = append(rows, it)
		}
		if len(rows) == 0 {
			continue
		}
		m.items = append(m.items, listItem{Key: "header:" + header, Label: header, Header: true})
		m.items = append(m.items, rows...)
	}
	if m.cursor >= len(m.items) {
		m.cursor = firstNonHeader(m.items)
	}
}

func firstNonHeader(items []listItem) int {
	for i, it := range items {
		if !it.Header {
			return i
		}
	}
	return 0
}

func (m *model) moveSettings(delta int) {
	if len(m.items) == 0 {
		return
	}
	i := m.cursor + delta
	for i >= 0 && i < len(m.items) {
		if !m.items[i].Header {
			m.cursor = i
			return
		}
		i += delta
	}
}

func (m *model) handleSettingsKey(msg tea.KeyMsg) tea.Cmd {
	if isSettingsCloseKey(msg) {
		m.closeSettings()
		return nil
	}
	if m.settingsEnumKey != "" {
		return m.handleSettingsEnumKey(msg)
	}
	if m.settingsSecretKey != "" {
		return m.handleSettingsSecretKey(msg)
	}
	if m.settingsEditKey != "" {
		return m.handleSettingsEditKey(msg)
	}
	if m.settingsFilter {
		return m.handleSettingsFilterKey(msg)
	}
	switch msg.String() {
	case "esc":
		m.closeSettings()
		return nil
	case "up", "k":
		m.moveSettings(-1)
		return nil
	case "down", "j":
		m.moveSettings(1)
		return nil
	case " ":
		if m.cursor < len(m.items) && !m.items[m.cursor].Header {
			if def, ok := lookupSetting(m.items[m.cursor].Key); ok && def.Kind == "bool" {
				return m.toggleSetting(def.Key)
			}
		}
		return nil
	case "enter":
		if m.cursor < len(m.items) && !m.items[m.cursor].Header {
			return m.activateSetting(m.items[m.cursor].Key)
		}
		return nil
	case "/", "i":
		m.settingsFilter = true
		return nil
	}
	return nil
}

func (m *model) closeSettings() {
	if m.settingsEnumKey != "" {
		m.revertSettingsEnumPreview()
	}
	m.overlay = overlayNone
	m.filter = ""
	m.settingsFilter = false
	m.settingsEnumKey = ""
	m.settingsSecretKey = ""
	m.settingsSecret = ""
	m.clearSettingsEditor()
}

func lookupSetting(key string) (settingDef, bool) {
	for _, d := range rustSettingsCatalog() {
		if d.Key == key {
			return d, true
		}
	}
	return settingDef{}, false
}

func (m *model) activateSetting(key string) tea.Cmd {
	def, ok := lookupSetting(key)
	if !ok {
		return m.toggleSetting(key)
	}
	switch def.Kind {
	case "enum":
		m.enterSettingsEnum(def)
		return nil
	case "secret":
		m.enterSettingsSecret(def)
		return nil
	case "string", "int":
		m.enterSettingsEditor(def)
		return nil
	default:
		return m.toggleSetting(key)
	}
}

func (m *model) handleSettingsFilterKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.filter = ""
		m.settingsFilter = false
		m.fillSettings()
		m.cursor = firstNonHeader(m.items)
		return nil
	case "enter":
		m.settingsFilter = false
		return nil
	case "up":
		m.moveSettings(-1)
		return nil
	case "down":
		m.moveSettings(1)
		return nil
	}
	if msg.Type == tea.KeyBackspace {
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.fillSettings()
			m.cursor = firstNonHeader(m.items)
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
		m.fillSettings()
		m.cursor = firstNonHeader(m.items)
		return nil
	}
	return nil
}

func isSettingsCloseKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "f2", "ctrl+,", "cmd+,", "super+,":
		return true
	}
	return msg.Type == tea.KeyF2
}

func (m *model) renderSettings(w int) string {
	if m.settingsEnumKey != "" {
		return m.renderSettingsEnum(w)
	}
	if m.settingsSecretKey != "" {
		return m.renderSettingsSecret(w)
	}
	if m.settingsEditKey != "" {
		return m.renderSettingsEditor(w)
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Settings") + "\n")
	if m.filter != "" || m.settingsFilter {
		cursor := ""
		if m.settingsFilter {
			cursor = "█"
		}
		b.WriteString(dimStyle.Render("filter: "+m.filter+cursor) + "\n")
	}
	b.WriteByte('\n')
	if len(m.items) == 0 {
		b.WriteString(dimStyle.Render("(no matches)") + "\n")
	}
	start := 0
	limit := 16
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	for i, it := range m.items {
		if i < start || i >= start+limit {
			continue
		}
		if it.Header {
			sep := strings.Repeat("─", max(4, w-len(it.Label)-4))
			b.WriteString(dimStyle.Bold(true).Render(" "+it.Label+" ") + dimStyle.Render(sep) + "\n")
			continue
		}
		line := it.Label
		if it.Detail != "" {
			line += "  " + dimStyle.Render(it.Detail)
		}
		if i == m.cursor {
			line = selStyle.Render("▸ " + it.Label + "  " + it.Detail)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	hint := "j/k  ·  space/enter  ·  / search  ·  i search  ·  F2/Esc close"
	if m.settingsFilter {
		hint = "type to filter  ·  ↑/↓ nav  ·  Backspace edit  ·  enter commit  ·  esc clear"
	}
	b.WriteString("\n" + dimStyle.Render(hint))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) settingValue(d settingDef) string {
	switch d.Key {
	case "compact_mode":
		return onOff(m.compactUI)
	case "screen_mode":
		return m.screenMd
	case "show_timestamps":
		return onOff(m.timestamps)
	case "show_timeline":
		return onOff(m.cfg.UI.ShowTimeline)
	case "page_flip_on_send":
		return onOff(m.pageFlip)
	case "simple_mode":
		return onOff(m.simpleMode)
	case "vim_mode":
		return onOff(m.vimMode)
	case "theme":
		return m.theme
	case "auto_dark_theme":
		return emptyAuto(m.cfg.UI.AutoDarkTheme)
	case "auto_light_theme":
		return emptyAuto(m.cfg.UI.AutoLightTheme)
	case "render_mermaid":
		return onOff(m.cfg.UI.RenderMermaid)
	case "show_thinking_blocks":
		return onOff(m.thinkingOn)
	case "prompt_suggestions":
		return onOff(m.suggest.enabled)
	case "respect_manual_folds":
		return onOff(m.cfg.UI.RespectFolds)
	case "group_tool_verbs":
		return onOff(m.groupTools)
	case "collapsed_edit_blocks":
		return onOff(m.cfg.UI.CollapsedEdits)
	case "invert_scroll":
		return onOff(m.cfg.UI.InvertScroll)
	case "keep_text_selection":
		return onOff(m.cfg.UI.KeepSelection)
	case "show_tips":
		return onOff(m.cfg.UI.ShowTips)
	case "ui.mouse_reporting_toggle":
		return onOff(m.mouseOn)
	case "combine_queued_prompts":
		return onOff(m.cfg.UI.CombineQueued)
	case "follow_up_behavior":
		return m.followUp
	case "confirm_before_rewind":
		return onOff(m.cfg.UI.ConfirmRewind)
	case "enter_steers":
		return onOff(m.cfg.UI.EnterSteers)
	case "multiline_mode":
		return onOff(m.multiline)
	case "swarm_mode":
		return onOff(m.swarm)
	case "permission_mode":
		return m.permModeName()
	case "remember_tool_approvals":
		return onOff(m.cfg.UI.RememberApprovals)
	case "code_mode":
		return m.codeMode
	case "plan_mode":
		return onOff(m.permMode == permPlan)
	case "default_selected_permission":
		if m.cfg.UI.DefaultPermChoice == "" {
			return "allow"
		}
		return m.cfg.UI.DefaultPermChoice
	case "stream_tool_calls":
		return onOff(m.cfg.UI.StreamToolCalls)
	case "hunk_tracker_mode":
		if m.cfg.UI.HunkTracker == "" {
			return "agent"
		}
		return m.cfg.UI.HunkTracker
	case "image_generation_provider":
		return m.imageGen
	case "default_model":
		return m.opts.Model.Name
	case "kimi_api_endpoint":
		if m.cfg.UI.KimiEndpoint == "" {
			return "platform"
		}
		return m.cfg.UI.KimiEndpoint
	case "recap_model":
		return emptyAuto(m.cfg.UI.RecapModel)
	case "memory_model":
		return emptyAuto(m.cfg.UI.MemoryModel)
	case "fork_secondary_model":
		return emptyAuto(m.cfg.UI.ForkSecondary)
	case "coding_data_sharing":
		if m.cfg.UI.PrivacyOptOut {
			return "opt-out"
		}
		return "opt-in"
	case "scroll_mode":
		if m.cfg.UI.ScrollMode == "" {
			return "line"
		}
		return m.cfg.UI.ScrollMode
	case "auto_update":
		return onOff(m.cfg.UI.AutoUpdate)
	case "voice_keybind_enabled":
		return onOff(m.voiceKeybindOn())
	case "voice_capture_mode":
		return m.voiceCaptureMode()
	case "memory.enabled":
		return onOff(m.cfg.MemoryEnabled())
	}
	if strings.HasSuffix(d.Key, "_api_key") || d.Kind == "secret" {
		if p, kimiCode, ok := settingSecretProvider(d.Key); ok {
			if _, found := auth.Lookup(p, kimiCode); found {
				return "saved"
			}
		}
		return "not set"
	}
	if m.cfg.Prefs != nil {
		if v, ok := m.cfg.Prefs[d.Key]; ok && v != "" {
			return v
		}
	}
	if d.Kind == "int" || d.Kind == "string" {
		return d.Default
	}
	if d.Kind == "bool" {
		return "off"
	}
	if len(d.Choices) > 0 {
		return d.Choices[0]
	}
	return ""
}

func (m *model) toggleSetting(key string) tea.Cmd {
	var def settingDef
	for _, d := range rustSettingsCatalog() {
		if d.Key == key {
			def = d
			break
		}
	}
	switch key {
	case "theme":
		m.openTheme()
		return nil
	case "default_model", "custom_models":
		m.openModels()
		return nil
	case "recap_model":
		m.openAuxModel("recap")
		return nil
	case "memory_model":
		m.openAuxModel("memory")
		return nil
	case "fork_secondary_model":
		m.openAuxModel("fork")
		return nil
	case "kimi_api_key", "kimi_code_api_key", "fireworks_api_key", "deepseek_api_key",
		"meta_api_key", "opencode_go_api_key", "wafer_api_key", "zai_api_key",
		"runinfra_api_key", "gemini_api_key", "openrouter_api_key", "perplexity_api_key":
		m.enterSettingsSecret(def)
		return nil
	case "vim_mode":
		m.vimMode = !m.vimMode
	case "show_timestamps":
		m.timestamps = !m.timestamps
	case "compact_mode":
		m.compactUI = !m.compactUI
	case "multiline_mode":
		m.multiline = !m.multiline
	case "screen_mode":
		if m.screenMd == modeMinimal {
			m.screenMd = modeFull
		} else {
			m.screenMd = modeMinimal
		}
	case "swarm_mode":
		m.swarm = !m.swarm
		if m.sess != nil {
			m.sess.Swarm = m.swarm
		}
	case "ui.mouse_reporting_toggle":
		m.mouseOn = !m.mouseOn
	case "show_thinking_blocks":
		m.thinkingOn = !m.thinkingOn
	case "prompt_suggestions":
		m.suggest.enabled = !m.suggest.enabled
		m.cfg.SetPrefBool("prompt_suggestions", m.suggest.enabled)
		if !m.suggest.enabled {
			m.suggest.clear()
		}
	case "memory.enabled":
		m.cfg.Features.Memory = !m.cfg.Features.Memory
	case "permission_mode":
		m.cyclePermMode()
	case "plan_mode":
		if m.permMode == permPlan {
			m.permMode = permAsk
			if m.sess != nil {
				m.sess.Plan.Exit()
			}
		} else {
			m.permMode = permPlan
			if m.sess != nil {
				m.sess.Plan.Enter(m.sess.Plan.PlanFile)
			}
		}
	case "remember_tool_approvals":
		m.cfg.UI.RememberApprovals = !m.cfg.UI.RememberApprovals
	case "code_mode":
		m.codeMode = cycleChoice(m.codeMode, []string{"direct", "code_mode", "code_mode_only"})
	case "follow_up_behavior":
		m.followUp = cycleChoice(m.followUp, []string{"queue", "steer"})
	case "image_generation_provider":
		m.imageGen = cycleChoice(m.imageGen, []string{"grok", "openai"})
	case "coding_data_sharing":
		m.cfg.UI.PrivacyOptOut = !m.cfg.UI.PrivacyOptOut
	case "show_timeline":
		m.cfg.UI.ShowTimeline = !m.cfg.UI.ShowTimeline
		m.layoutChat()
	case "page_flip_on_send":
		m.pageFlip = !m.pageFlip
		m.cfg.UI.PageFlipOnSend = m.pageFlip
	case "simple_mode":
		m.simpleMode = !m.simpleMode
		m.cfg.UI.SimpleMode = m.simpleMode
	case "render_mermaid":
		m.cfg.UI.RenderMermaid = !m.cfg.UI.RenderMermaid
	case "respect_manual_folds":
		m.cfg.UI.RespectFolds = !m.cfg.UI.RespectFolds
	case "group_tool_verbs":
		m.groupTools = !m.groupTools
		m.cfg.UI.GroupToolVerbs = m.groupTools
		m.expandedGroups = map[string]bool{}
	case "collapsed_edit_blocks":
		m.cfg.UI.CollapsedEdits = !m.collapsedEditBlocks()
		m.cfg.SetPrefBool("collapsed_edit_blocks", m.cfg.UI.CollapsedEdits)
		fold := m.cfg.UI.CollapsedEdits
		for i := range m.blocks {
			if m.blocks[i].kind == blockTool && classifyTool(m.blocks[i].title) == verbEdit {
				m.blocks[i].folded = fold
			}
		}
	case "invert_scroll":
		m.cfg.UI.InvertScroll = !m.cfg.UI.InvertScroll
	case "keep_text_selection":
		m.cfg.UI.KeepSelection = !m.cfg.UI.KeepSelection
	case "show_tips":
		m.cfg.UI.ShowTips = !m.cfg.UI.ShowTips
	case "combine_queued_prompts":
		m.cfg.UI.CombineQueued = !m.cfg.UI.CombineQueued
	case "confirm_before_rewind":
		m.cfg.UI.ConfirmRewind = !m.cfg.UI.ConfirmRewind
	case "enter_steers":
		m.cfg.UI.EnterSteers = !m.cfg.UI.EnterSteers
	case "stream_tool_calls":
		m.cfg.UI.StreamToolCalls = !m.cfg.UI.StreamToolCalls
	case "auto_update":
		m.cfg.UI.AutoUpdate = !m.cfg.UI.AutoUpdate
	case "voice_keybind_enabled":
		m.cfg.UI.VoiceKeybind = !m.cfg.UI.VoiceKeybind
	case "kimi_api_endpoint":
		m.cfg.UI.KimiEndpoint = cycleChoice(m.cfg.UI.KimiEndpoint, []string{"platform", "code"})
	case "scroll_mode":
		m.cfg.UI.ScrollMode = cycleChoice(m.cfg.UI.ScrollMode, []string{"line", "block", "page"})
	case "hunk_tracker_mode":
		m.cfg.UI.HunkTracker = cycleChoice(m.cfg.UI.HunkTracker, []string{"off", "agent", "all"})
	case "default_selected_permission":
		m.cfg.UI.DefaultPermChoice = cycleChoice(m.cfg.UI.DefaultPermChoice, []string{"allow", "always", "deny"})
	case "auto_dark_theme":
		m.cfg.UI.AutoDarkTheme = cycleChoice(m.cfg.UI.AutoDarkTheme, []string{"groknight", "tokyonight", "rosepine-moon", "oscura-midnight"})
	case "auto_light_theme":
		m.cfg.UI.AutoLightTheme = cycleChoice(m.cfg.UI.AutoLightTheme, []string{"grokday"})
	default:
		if m.cfg.Prefs == nil {
			m.cfg.Prefs = config.Prefs{}
		}
		if def.Kind == "bool" {
			cur := m.cfg.Prefs[key]
			if cur == "true" || cur == "on" {
				m.setPref(key, "false")
			} else {
				m.setPref(key, "true")
			}
		} else if def.Kind == "enum" && len(def.Choices) > 0 {
			m.setPref(key, cycleChoice(m.cfg.Prefs[key], def.Choices))
		}
	}
	m.persistUI()
	cur := m.cursor
	filt := m.filter
	m.fillSettings()
	m.filter = filt
	if cur < len(m.items) {
		m.cursor = cur
	}
	if m.cursor < len(m.items) && m.items[m.cursor].Header {
		m.moveSettings(1)
	}
	return nil
}

func (m *model) setPref(key, val string) {
	if m.cfg.Prefs == nil {
		m.cfg.Prefs = config.Prefs{}
	}
	m.cfg.Prefs[key] = val
}

func cycleChoice(cur string, choices []string) string {
	if len(choices) == 0 {
		return cur
	}
	for i, c := range choices {
		if c == cur {
			return choices[(i+1)%len(choices)]
		}
	}
	return choices[0]
}
