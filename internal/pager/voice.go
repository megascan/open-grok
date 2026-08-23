package pager

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/voice"
)

// liveVoiceCapture starts Linux mic + streaming STT when a recorder is on PATH.
// Pager tests turn this off so they never spawn pw-record/arecord.
var liveVoiceCapture = true

// startVoiceSession is voice.Start; tests replace it with a fake pipeline.
var startVoiceSession = voice.Start

var detectVoiceRecorder = voice.DetectRecorder

func defaultResolveSTTBearer() (string, error) {
	b, _, err := auth.ResolveBearer(types.ProviderXAI, auth.EnvKeys(types.ProviderXAI), false)
	return b, err
}

var resolveSTTBearer = defaultResolveSTTBearer

// voiceState is the dictation overlay. Live capture fills Interim/Transcript
// from xAI streaming STT; keys can still edit the committed transcript.
type voiceState struct {
	Active     bool
	Transcript string
	Interim    string
	Err        string
	Hint       string
	Recorder   string
	TypedOnly  bool
	stop       func()
	events     <-chan voice.Event
}

type voiceEventMsg struct {
	ev voice.Event
}

type voiceEndedMsg struct{}

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
	return m.startVoice()
}

func (m *model) startVoice() tea.Cmd {
	m.voice = &voiceState{Active: true}
	m.overlay = overlayVoice
	m.focusScroll = false
	m.ta.Blur()
	if !liveVoiceCapture {
		return nil
	}
	return m.beginLiveVoice()
}

func (m *model) beginLiveVoice() tea.Cmd {
	if m.voice == nil {
		return nil
	}
	if _, ok := detectVoiceRecorder(); !ok {
		m.voice.TypedOnly = true
		m.voice.Err = voice.ErrNoRecorder
		return nil
	}
	if rec, ok := detectVoiceRecorder(); ok {
		m.voice.Recorder = rec.Name()
	}
	bearer, err := resolveSTTBearer()
	if err != nil {
		m.voice.TypedOnly = true
		m.voice.Err = voice.ErrAuth
		return nil
	}
	cfg := voice.ConfigFromEnv()
	if m.cfg.Prefs != nil {
		if lang := strings.TrimSpace(m.cfg.Prefs["voice_stt_language"]); lang != "" {
			cfg.Language = lang
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := startVoiceSession(ctx, cfg, bearer)
	if err != nil {
		cancel()
		m.voice.TypedOnly = true
		m.voice.Err = err.Error()
		return nil
	}
	m.voice.stop = func() {
		cancel()
		sess.Stop()
	}
	m.voice.events = sess.Events
	m.voice.TypedOnly = false
	return listenVoice(sess.Events)
}

func listenVoice(ch <-chan voice.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return voiceEndedMsg{}
		}
		return voiceEventMsg{ev: ev}
	}
}

func (m *model) handleVoiceEvent(msg voiceEventMsg) tea.Cmd {
	if m.voice == nil || !m.voice.Active {
		return nil
	}
	switch msg.ev.Kind {
	case voice.EventInterim:
		m.voice.Interim = msg.ev.Text
		m.voice.Err = ""
	case voice.EventFinal:
		text := strings.TrimSpace(msg.ev.Text)
		if text != "" {
			if strings.TrimSpace(m.voice.Transcript) == "" {
				m.voice.Transcript = text
			} else {
				m.voice.Transcript = strings.TrimSpace(m.voice.Transcript) + " " + text
			}
		}
		m.voice.Interim = ""
	case voice.EventError:
		m.voice.Err = msg.ev.Text
		m.voice.Hint = msg.ev.Hint
		m.voice.Interim = ""
		if m.voice.stop != nil {
			m.voice.stop()
			m.voice.stop = nil
		}
		m.voice.events = nil
		return nil
	}
	return listenVoice(m.voice.events)
}

func (m *model) stopVoice() {
	if m.voice != nil {
		if m.voice.stop != nil {
			m.voice.stop()
			m.voice.stop = nil
		}
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
		if extra := strings.TrimSpace(m.voice.Interim); extra != "" {
			if text == "" {
				text = extra
			} else {
				text += " " + extra
			}
		}
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
	if m.voice.Interim != "" {
		if body == "" {
			body = m.voice.Interim
		} else {
			body += " " + m.voice.Interim
		}
	}
	if body == "" && m.voice.Err != "" {
		body = m.voice.Err
	}
	if body == "" {
		if m.voice.TypedOnly || m.voice.Recorder == "" {
			body = "Listening… type to dictate — " + voice.ErrNoRecorder
		} else {
			body = "Listening…"
		}
	}
	hint := "enter send  ·  esc cancel  ·  Ctrl+Space/F8 stop  ·  " + m.voiceCaptureMode()
	if m.voice.Hint != "" {
		hint = m.voice.Hint + "  ·  " + hint
	}
	return panelStyle.Width(max(20, m.width-2)).Render(
		okStyle.Render("● Recording") + "\n" + body + "█\n" + dimStyle.Render(hint),
	)
}
