package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// voiceState is the dictation overlay. There is no microphone in this pager;
// keys type into the transcript (the recording banner is the only feedback).
type voiceState struct {
	Active     bool
	Transcript string
	Interim    string
}

func (m *model) voiceKeybindOn() bool {
	return m.cfg.PrefBool("voice_keybind_enabled", m.cfg.UI.VoiceKeybind, true)
}

func (m *model) voiceCaptureMode() string {
	if m.cfg.Prefs != nil {
		mode := strings.ToLower(strings.TrimSpace(m.cfg.Prefs["voice_capture_mode"]))
		if mode == "hold" {
			// Hold-to-talk needs key-release reports (Kitty). Bubble Tea sessions
			// fall back to toggle, matching the Rust chord when releases are absent.
			return "toggle"
		}
		if mode != "" {
			return mode
		}
	}
	return "toggle"
}

func isVoiceChord(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "ctrl+space", "ctrl+@", "f8":
		return true
	default:
		return false
	}
}

func (m *model) toggleVoice() tea.Cmd {
	if m.screen == screenWelcome {
		return nil
	}
	if m.voice != nil && m.voice.Active {
		m.stopVoice()
		return nil
	}
	m.startVoice()
	return nil
}

func (m *model) startVoice() {
	m.voice = &voiceState{Active: true}
	m.overlay = overlayVoice
	m.focusScroll = false
	m.ta.Blur()
}

func (m *model) stopVoice() {
	if m.voice != nil {
		m.voice.Active = false
	}
	m.voice = nil
	if m.overlay == overlayVoice {
		m.overlay = overlayNone
	}
	if m.screen == screenChat && !m.focusScroll {
		m.ta.Focus()
	}
}

func (m *model) handleVoiceKey(msg tea.KeyMsg) tea.Cmd {
	if m.voice == nil || !m.voice.Active {
		return nil
	}
	if isVoiceChord(msg) {
		m.stopVoice()
		return nil
	}
	switch msg.String() {
	case "esc":
		m.stopVoice()
		return nil
	case "enter":
		text := strings.TrimSpace(m.voice.Transcript)
		m.stopVoice()
		if text == "" {
			return nil
		}
		if m.screen == screenDashboard {
			m.dashDraft = text
			m.dashListFocus = false
			return m.dashEnter(true)
		}
		if strings.HasPrefix(text, "/") {
			return m.handleSlash(text)
		}
		return m.submit(text)
	case "backspace":
		if n := len(m.voice.Transcript); n > 0 {
			m.voice.Transcript = m.voice.Transcript[:n-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.voice.Transcript += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			m.voice.Transcript += " "
		}
	}
	return nil
}

func (m *model) renderVoiceBanner() string {
	if m.voice == nil || !m.voice.Active {
		return ""
	}
	body := strings.TrimSpace(m.voice.Transcript)
	if body == "" {
		body = m.voice.Interim
	}
	if body == "" {
		body = "Listening… type to dictate (no microphone in this pager)"
	}
	hint := "enter send  ·  esc cancel  ·  Ctrl+Space/F8 stop  ·  " + m.voiceCaptureMode()
	return panelStyle.Width(max(20, m.width-2)).Render(
		okStyle.Render("● Recording") + "\n" + body + "█\n" + dimStyle.Render(hint),
	)
}
