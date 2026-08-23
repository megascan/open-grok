package pager

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/voice"
)

func TestVoiceOverlayLiveSTTEvents(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	liveVoiceCapture = true
	t.Cleanup(func() { liveVoiceCapture = false })
	detectVoiceRecorder = func() (voice.Recorder, bool) { return voice.FakeRecorder("pw-record"), true }
	resolveSTTBearer = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() {
		detectVoiceRecorder = voice.DetectRecorder
		resolveSTTBearer = defaultResolveSTTBearer
	})

	events := make(chan voice.Event, 4)
	startVoiceSession = func(ctx context.Context, cfg voice.Config, bearer string) (*voice.Session, error) {
		return &voice.Session{Events: events}, nil
	}
	t.Cleanup(func() { startVoiceSession = voice.Start })

	cmd := m.startVoice()
	if m.overlay != overlayVoice || m.voice == nil {
		t.Fatal("overlay")
	}
	events <- voice.Event{Kind: voice.EventInterim, Text: "hello there"}
	msg := cmd()
	ev, ok := msg.(voiceEventMsg)
	if !ok {
		t.Fatalf("%T", msg)
	}
	next := m.handleVoiceEvent(ev)
	if m.voice.Interim != "hello there" {
		t.Fatalf("interim %q", m.voice.Interim)
	}
	body := stripANSI(m.renderVoiceBanner())
	if !strings.Contains(body, "Recording") || !strings.Contains(body, "hello there") {
		t.Fatalf("%s", body)
	}
	if strings.Contains(body, "no microphone") {
		t.Fatalf("live banner still typed-only: %s", body)
	}

	events <- voice.Event{Kind: voice.EventFinal, Text: "hello there"}
	msg = next()
	ev, ok = msg.(voiceEventMsg)
	if !ok {
		t.Fatalf("%T", msg)
	}
	_ = m.handleVoiceEvent(ev)
	if m.voice.Transcript != "hello there" {
		t.Fatalf("final %q", m.voice.Transcript)
	}
	if m.voice.Interim != "" {
		t.Fatalf("interim leftover %q", m.voice.Interim)
	}

	send := m.handleVoiceKey(tea.KeyMsg{Type: tea.KeyEnter})
	if send == nil {
		t.Fatal("enter should submit")
	}
	if m.overlay == overlayVoice {
		t.Fatal("enter must close overlay")
	}
}

func TestVoiceSettingsLanguageCatalog(t *testing.T) {
	codes := voice.SettingsLanguageCodes()
	if codes[0] != "auto" || !containsStr(codes, "fil") || containsStr(codes, "zh") {
		t.Fatalf("%v", codes)
	}
	stt, ok := lookupSetting("voice_stt_language")
	if !ok {
		t.Fatal("voice_stt_language missing")
	}
	if len(stt.Choices) != len(codes) {
		t.Fatalf("choices %d vs %d", len(stt.Choices), len(codes))
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
