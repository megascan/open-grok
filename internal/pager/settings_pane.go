package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
)

const (
	pickerDotFilled             = "\u25cf"
	pickerDotEmpty              = "\u25cb"
	permAccentBar               = "\u2503"
	permFollowupLabel           = "No, reject (type to add feedback)"
	questionFreeformPlaceholder = "Type your answer here"
	peekOtherPlaceholder        = "Other (type your own answer)"
	welcomeTrustWarningLine1    = "Open Grok may run or modify contents in this directory,"
	welcomeTrustWarningLine2    = "posing security risks."
	welcomeTrustQuestion        = "Do you trust the contents of this directory?"
	welcomeUnsignedPrompt       = "Choose the account that will power this session."
	feedbackPlaceholder         = "Please provide as much detail as possible."
	feedbackBlockedQuestion     = "Finish answering the current question first"
	feedbackBlockedOverlay      = "Close or answer what's open before sending feedback"
	planWaitingLabel            = "Waiting on plan approval"
	planEmptyTitle              = "No plan written — approve or request changes"
	privacyBannerTitle          = "Help improve Open Grok"
	privacyBannerCompactTitle   = "Improve Open Grok"
	privacyBannerDesc           = "Off by default. Opt-in to allow SpaceXAI to retain coding data, e.g., prompts, traces, & metrics, for training and debugging purposes. Change anytime via settings."
	privacyBannerLegal          = "Read Terms and Privacy Policy."
	privacyOptInLabel           = "[Opt in]"
	privacyOptOutLabel          = "[Opt out]"
	shortcutsModalTitle         = "Keyboard Shortcuts"
	worktreeDialogTitle         = "New Worktree"
	worktreeNamePrefix          = "Name (optional): "
	deleteSessionQuestion       = "Delete this session permanently?"
	deleteSessionApplyLabel     = "Delete"
	deleteSessionApplyDesc      = "Remove history and return home"
	deleteSessionCancelDesc     = "Keep the session"
	doctorFixQuestion           = "Apply this fix?"
	doctorFixApplyDesc          = "Make the changes shown above."
	doctorFixCancelDesc         = "Do not change the configuration."
	resumePendingHint           = "y confirm delete  ·  n cancel"
	resumePickerHint            = "↑↓ nav  ·  / search  ·  f filter  ·  d delete"
	usageShortcutHint           = "Tab switch  ·  ↑/↓ scroll  ·  c copy session ID  ·  Esc close"
)

func writeAccent(b *strings.Builder, s string) {
	b.WriteString(titleStyle.Render(permAccentBar) + "  " + s + "\n")
}

func writeAccentLines(b *strings.Builder, s string) {
	if s == "" {
		writeAccent(b, "")
		return
	}
	for _, line := range strings.Split(s, "\n") {
		writeAccent(b, line)
	}
}

func radioRow(hotkey, label string, cursor bool) string {
	marker := "(" + pickerDotEmpty + ")"
	if cursor {
		marker = "(" + pickerDotFilled + ")"
	}
	line := hotkey + " " + marker + " " + label
	if cursor {
		return selStyle.Render(line)
	}
	return line
}

func (m *model) enterSettingsEnum(def settingDef) {
	m.settingsEnumKey = def.Key
	m.settingsEnumOrig = m.settingValue(def)
	m.settingsEnumIdx = 0
	for i, c := range def.Choices {
		if enumChoiceMatches(def.Key, m.settingsEnumOrig, c) {
			m.settingsEnumIdx = i
			break
		}
	}
	m.previewSettingsEnum(def)
}

func enumChoiceMatches(key, current, choice string) bool {
	if current == choice {
		return true
	}
	if key == "permission_mode" {
		switch current {
		case "always":
			return choice == "always-approve"
		case "plan":
			return choice == "ask"
		}
	}
	return false
}

func settingsEnumSupportsPreview(key string) bool {
	switch key {
	case "theme", "auto_dark_theme", "auto_light_theme":
		return true
	default:
		return false
	}
}

func (m *model) previewSettingsEnum(def settingDef) {
	if !settingsEnumSupportsPreview(def.Key) || m.settingsEnumIdx >= len(def.Choices) {
		return
	}
	choice := def.Choices[m.settingsEnumIdx]
	if def.Key == "theme" || def.Key == "auto_dark_theme" || def.Key == "auto_light_theme" {
		applyTheme(resolveTheme(choice))
	}
}

func (m *model) revertSettingsEnumPreview() {
	if m.settingsEnumKey == "" {
		return
	}
	if settingsEnumSupportsPreview(m.settingsEnumKey) {
		applyTheme(resolveTheme(m.settingsEnumOrig))
	}
}

func (m *model) handleSettingsEnumKey(msg tea.KeyMsg) tea.Cmd {
	def, ok := lookupSetting(m.settingsEnumKey)
	if !ok {
		m.settingsEnumKey = ""
		return nil
	}
	switch msg.String() {
	case "esc":
		m.revertSettingsEnumPreview()
		m.settingsEnumKey = ""
		m.settingsEnumOrig = ""
		return nil
	case "up", "k":
		if m.settingsEnumIdx > 0 {
			m.settingsEnumIdx--
			m.previewSettingsEnum(def)
		}
		return nil
	case "down", "j":
		if m.settingsEnumIdx+1 < len(def.Choices) {
			m.settingsEnumIdx++
			m.previewSettingsEnum(def)
		}
		return nil
	case "enter":
		if m.settingsEnumIdx < len(def.Choices) {
			choice := def.Choices[m.settingsEnumIdx]
			m.settingsEnumKey = ""
			m.settingsEnumOrig = ""
			return m.applySettingChoice(def.Key, choice)
		}
		m.settingsEnumKey = ""
		return nil
	}
	return nil
}

func (m *model) renderSettingsEnum(w int) string {
	def, ok := lookupSetting(m.settingsEnumKey)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(def.Label) + "\n")
	b.WriteString(dimStyle.Render(def.Key) + "\n\n")
	for i, c := range def.Choices {
		dot := pickerDotEmpty
		line := "  " + pickerDotEmpty + "  " + c
		if i == m.settingsEnumIdx {
			dot = pickerDotFilled
			line = selStyle.Render("  " + dot + "  " + c)
		} else {
			line = "  " + dot + "  " + c
		}
		b.WriteString(line + "\n")
	}
	nav := "↑/↓ nav"
	esc := "Esc cancel"
	if settingsEnumSupportsPreview(def.Key) {
		nav = "↑/↓ try"
		esc = "Esc revert"
	}
	b.WriteString("\n" + dimStyle.Render(nav+"  ·  Enter select  ·  "+esc))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) applySettingChoice(key, val string) tea.Cmd {
	switch key {
	case "screen_mode":
		m.screenMd = val
	case "code_mode":
		m.codeMode = val
	case "follow_up_behavior":
		m.followUp = val
	case "image_generation_provider":
		m.imageGen = val
	case "kimi_api_endpoint":
		m.cfg.UI.KimiEndpoint = val
	case "scroll_mode":
		m.cfg.UI.ScrollMode = val
	case "hunk_tracker_mode":
		m.cfg.UI.HunkTracker = val
	case "default_selected_permission":
		m.cfg.UI.DefaultPermChoice = val
	case "auto_dark_theme":
		m.cfg.UI.AutoDarkTheme = val
		applyTheme(resolveTheme(val))
	case "auto_light_theme":
		m.cfg.UI.AutoLightTheme = val
		applyTheme(resolveTheme(val))
	case "coding_data_sharing":
		m.cfg.UI.PrivacyOptOut = val == "opt-out"
	case "voice_capture_mode":
		m.cfg.SetPrefBool("voice_capture_mode_hold", val == "hold")
		m.setPref("voice_capture_mode", val)
	case "permission_mode":
		m.applyPermissionModeChoice(val)
	case "theme":
		m.theme = canonicalThemeName(val)
		if m.theme == "" {
			m.theme = val
		}
		applyTheme(resolveTheme(m.theme))
		saveTheme(m.theme)
	default:
		m.setPref(key, val)
	}
	m.persistUI()
	m.fillSettings()
	return nil
}

func (m *model) applyPermissionModeChoice(val string) {
	switch val {
	case "always-approve", "always":
		m.permMode = permAlways
		if m.sess != nil {
			m.sess.YOLO = true
			m.sess.PermMode = "always"
		}
	case "auto":
		m.permMode = permAuto
		if m.sess != nil {
			m.sess.YOLO = false
			m.sess.PermMode = "auto"
		}
	default:
		m.permMode = permAsk
		if m.sess != nil {
			m.sess.YOLO = false
			m.sess.PermMode = "ask"
		}
	}
}

func (m *model) enterSettingsSecret(def settingDef) {
	m.settingsSecretKey = def.Key
	m.settingsSecret = ""
}

func (m *model) handleSettingsSecretKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.settingsSecretKey = ""
		m.settingsSecret = ""
		return nil
	case "enter":
		key := strings.TrimSpace(m.settingsSecret)
		setting := m.settingsSecretKey
		m.settingsSecretKey = ""
		m.settingsSecret = ""
		if key == "" {
			return nil
		}
		return m.commitSettingsSecret(setting, key)
	}
	if msg.Type == tea.KeyBackspace {
		if m.settingsSecret != "" {
			m.settingsSecret = m.settingsSecret[:len(m.settingsSecret)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.settingsSecret += string(msg.Runes)
	}
	return nil
}

func (m *model) renderSettingsSecret(w int) string {
	def, ok := lookupSetting(m.settingsSecretKey)
	label := m.settingsSecretKey
	if ok {
		label = def.Label
	}
	shown := strings.Repeat("\u2022", len(m.settingsSecret))
	if shown == "" {
		shown = dimStyle.Render("(hidden)")
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(label) + "\n")
	b.WriteString(dimStyle.Render("Paste an API key. It is written owner-only under ~/.entropy.") + "\n\n")
	b.WriteString("key: " + shown + "\n\n")
	b.WriteString(dimStyle.Render("Enter commit  ·  Esc cancel  ·  never commit this key"))
	return modalBox.Width(w).Render(b.String())
}

func settingSecretProvider(key string) (types.Provider, bool, bool) {
	switch key {
	case "kimi_api_key":
		return types.ProviderKimi, false, true
	case "kimi_code_api_key":
		return types.ProviderKimi, true, true
	case "fireworks_api_key":
		return types.ProviderFireworks, false, true
	case "deepseek_api_key":
		return types.ProviderDeepSeek, false, true
	case "meta_api_key":
		return types.ProviderMeta, false, true
	case "opencode_go_api_key":
		return types.ProviderOpenCodeGo, false, true
	case "wafer_api_key":
		return types.ProviderWafer, false, true
	case "zai_api_key":
		return types.ProviderZAI, false, true
	case "runinfra_api_key":
		return types.ProviderRunInfra, false, true
	case "gemini_api_key":
		return types.ProviderGemini, false, true
	case "openrouter_api_key":
		return types.ProviderOpenRouter, false, true
	default:
		return "", false, false
	}
}

func (m *model) commitSettingsSecret(setting, key string) tea.Cmd {
	if setting == "perplexity_api_key" {
		m.setPref(setting, "saved")
		m.persistUI()
		m.appendSys("\u2713 Perplexity API key saved")
		m.fillSettings()
		return nil
	}
	p, kimiCode, ok := settingSecretProvider(setting)
	if !ok {
		m.appendSys("unknown secret setting")
		return nil
	}
	if err := auth.SaveAPIKey(p, key, kimiCode); err != nil {
		m.appendSys(err.Error())
		return nil
	}
	m.appendSys("\u2713 " + p.DisplayName() + " API key saved")
	m.fillSettings()
	return nil
}
