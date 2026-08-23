package pager

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Settings › label breadcrumb, matching the Rust settings-modal sub-pane.
const settingsEditorBreadcrumb = "Settings \u203a "

const (
	intStepperWideSmall = int64(5)
	intStepperWideLarge = int64(10)
	stringPlaceholder   = "<type a value>"
)

func (m *model) clearSettingsEditor() {
	m.settingsEditKey = ""
	m.settingsEditBuf = ""
	m.settingsEditErr = ""
	m.settingsEditKind = ""
	m.settingsEditMin = 0
	m.settingsEditMax = 0
}

func (m *model) enterSettingsEditor(def settingDef) {
	m.settingsEditKey = def.Key
	m.settingsEditKind = def.Kind
	m.settingsEditErr = ""
	m.settingsEditMin = def.Min
	m.settingsEditMax = def.Max
	cur := m.settingValue(def)
	if def.Kind == "int" {
		n, ok := parseSettingInt(cur)
		if !ok {
			n, _ = parseSettingInt(def.Default)
		}
		if n < def.Min {
			n = def.Min
		}
		if n > def.Max {
			n = def.Max
		}
		m.settingsEditBuf = strconv.FormatInt(n, 10)
		return
	}
	m.settingsEditBuf = cur
}

func parseSettingInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// intStepSizes is the Rust settings-modal stepper policy
// (views/settings_modal/render.rs int_step_sizes).
func intStepSizes(min, max int64) (small, large int64) {
	span := max - min
	if span < 0 {
		span = 0
	}
	switch {
	case span <= 20:
		large = span / 5
		if large < 1 {
			large = 1
		}
		return 1, large
	case span <= 100:
		return 1, 5
	default:
		return intStepperWideSmall, intStepperWideLarge
	}
}

func intStepFooterLabels(min, max int64) (string, string) {
	small, large := intStepSizes(min, max)
	switch {
	case small == 1 && large == 1:
		return "\u2191/\u2193 +/-1", "\u2190/\u2192 +/-1"
	case small == 1 && large == 5:
		return "\u2191/\u2193 +/-1", "\u2190/\u2192 +/-5"
	case small == 5 && large == 10:
		return "\u2191/\u2193 +/-5", "\u2190/\u2192 +/-10"
	case small == 1:
		return "\u2191/\u2193 +/-1", "\u2190/\u2192 step"
	case small == 5:
		return "\u2191/\u2193 +/-5", "\u2190/\u2192 step"
	default:
		return "\u2191/\u2193 step", "\u2190/\u2192 step"
	}
}

func (m *model) handleSettingsEditKey(msg tea.KeyMsg) tea.Cmd {
	if m.settingsEditKind == "int" {
		return m.handleSettingsIntKey(msg)
	}
	return m.handleSettingsStringKey(msg)
}

func (m *model) handleSettingsIntKey(msg tea.KeyMsg) tea.Cmd {
	small, large := intStepSizes(m.settingsEditMin, m.settingsEditMax)
	step := func(delta int64) {
		cur, ok := parseSettingInt(m.settingsEditBuf)
		if !ok {
			cur = m.settingsEditMin
		}
		next := cur + delta
		if next < m.settingsEditMin {
			next = m.settingsEditMin
		}
		if next > m.settingsEditMax {
			next = m.settingsEditMax
		}
		m.settingsEditBuf = strconv.FormatInt(next, 10)
		m.settingsEditErr = ""
	}
	switch msg.String() {
	case "esc":
		m.clearSettingsEditor()
		return nil
	case "enter":
		return m.commitSettingsEditor()
	case "up", "k":
		step(small)
	case "down", "j":
		step(-small)
	case "right", "l":
		step(large)
	case "left", "h":
		step(-large)
	}
	return nil
}

func (m *model) handleSettingsStringKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.clearSettingsEditor()
		return nil
	case "enter":
		return m.commitSettingsEditor()
	case "up", "down", "pgup", "pgdown", "tab", "shift+tab":
		return nil
	}
	if msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH {
		if m.settingsEditBuf != "" {
			_, size := utf8.DecodeLastRuneInString(m.settingsEditBuf)
			m.settingsEditBuf = m.settingsEditBuf[:len(m.settingsEditBuf)-size]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.settingsEditBuf += string(msg.Runes)
	} else if msg.Type == tea.KeySpace {
		m.settingsEditBuf += " "
	}
	return nil
}

func (m *model) commitSettingsEditor() tea.Cmd {
	key := m.settingsEditKey
	kind := m.settingsEditKind
	buf := m.settingsEditBuf
	min, max := m.settingsEditMin, m.settingsEditMax
	if kind == "int" {
		n, ok := parseSettingInt(buf)
		if !ok || n < min || n > max {
			m.settingsEditErr = fmt.Sprintf("must be %d\u2013%d", min, max)
			return nil
		}
		buf = strconv.FormatInt(n, 10)
	}
	m.clearSettingsEditor()
	return m.applySettingChoice(key, buf)
}

func (m *model) renderSettingsEditor(w int) string {
	def, ok := lookupSetting(m.settingsEditKey)
	label := m.settingsEditKey
	if ok {
		label = def.Label
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(settingsEditorBreadcrumb+label) + "\n\n")
	if m.settingsEditKind == "int" {
		b.WriteString(lipgloss.NewStyle().Width(max(20, w-8)).Align(lipgloss.Center).Render(
			"\u2039  "+m.settingsEditBuf+"  \u203a",
		) + "\n")
		if m.settingsEditErr != "" {
			b.WriteString("\n" + errStyle.Render(m.settingsEditErr) + "\n")
		}
		small, large := intStepFooterLabels(m.settingsEditMin, m.settingsEditMax)
		b.WriteString("\n" + dimStyle.Render(small+"   "+large+"   Enter save   Esc cancel   d reset"))
		return modalBox.Width(w).Render(b.String())
	}
	shown := m.settingsEditBuf + "\u2588"
	if m.settingsEditBuf == "" {
		shown = dimStyle.Render(stringPlaceholder) + "\u2588"
	}
	b.WriteString(shown + "\n")
	if m.settingsEditErr != "" {
		b.WriteString(errStyle.Render(m.settingsEditErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Enter save  \u00b7  Esc cancel"))
	return modalBox.Width(w).Render(b.String())
}
