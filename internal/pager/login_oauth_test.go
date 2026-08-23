package pager

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/oauth"
	"github.com/megascan/open-grok/internal/types"
)

func stubCodexOAuth(t *testing.T, waitCh <-chan struct{}) {
	t.Helper()
	prev := startCodexOAuth
	t.Cleanup(func() { startCodexOAuth = prev })
	startCodexOAuth = func(ctx context.Context) (string, func(context.Context) (oauth.TokenResponse, error), func(), error) {
		return "https://auth.example/codex", func(ctx context.Context) (oauth.TokenResponse, error) {
			if waitCh == nil {
				return oauth.TokenResponse{IDToken: "id", AccessToken: "acc", RefreshToken: "ref"}, nil
			}
			select {
			case <-ctx.Done():
				return oauth.TokenResponse{}, ctx.Err()
			case <-waitCh:
				return oauth.TokenResponse{IDToken: "id", AccessToken: "acc", RefreshToken: "ref"}, nil
			}
		}, func() {}, nil
	}
}

func TestCodexLoginStartsBrowserOAuth(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	waitCh := make(chan struct{})
	stubCodexOAuth(t, waitCh)

	cmd := m.openLogin("codex")
	if !m.loginOAuthWaiting {
		t.Fatal("expected Codex OAuth to start")
	}
	body := m.loginBody()
	if !strings.Contains(body, "Opening your browser to connect OpenAI Codex") {
		t.Fatalf("waiting copy missing:\n%s", body)
	}
	if !strings.Contains(body, "https://auth.example/codex") {
		t.Fatalf("authorize URL missing:\n%s", body)
	}
	if cmd == nil {
		t.Fatal("expected wait command")
	}
	close(waitCh)
	msg, ok := cmd().(loginOAuthDoneMsg)
	if !ok || msg.err != nil {
		t.Fatalf("done %+v ok=%v", msg, ok)
	}
	m.handleLoginOAuthDone(msg)
	if m.overlay != overlayNone {
		t.Fatalf("overlay %d", m.overlay)
	}
	cred, ok := auth.Lookup(types.ProviderCodex, false)
	if !ok || cred.Key != "acc" {
		t.Fatalf("stored %+v ok=%v", cred, ok)
	}
	if cred.AuthMode != "chatgpt" {
		t.Fatalf("auth mode %q", cred.AuthMode)
	}
}

func TestCodexPickerSelectStartsOAuth(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	stubCodexOAuth(t, make(chan struct{}))
	m.openLogin("")
	idx := -1
	for i, it := range m.items {
		if it.Key == string(types.ProviderCodex) {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("codex missing: %+v", m.items)
	}
	m.cursor = idx
	cmd := m.handleLoginKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected oauth wait cmd")
	}
	if m.loginP != types.ProviderCodex || !m.loginOAuthWaiting {
		t.Fatalf("loginP=%s waiting=%v", m.loginP, m.loginOAuthWaiting)
	}
}

func TestCodexOAuthEscCancels(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	stubCodexOAuth(t, make(chan struct{}))
	m.openLogin("codex")
	if !m.loginOAuthWaiting {
		t.Fatal("expected waiting")
	}
	m.handleLoginKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.loginOAuthWaiting {
		t.Fatal("esc must cancel oauth")
	}
	if m.loginP != "" {
		t.Fatalf("expected picker, loginP=%s", m.loginP)
	}
}

func TestCodexAPIKeyStillSavesWhileOAuthWaits(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.screen = screenChat
	stubCodexOAuth(t, make(chan struct{}))
	m.openLogin("codex")
	m.keyBuf = "sk-codex-test-key"
	m.handleLoginKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.loginOAuthWaiting {
		t.Fatal("saving a key must stop oauth")
	}
	cred, ok := auth.Lookup(types.ProviderCodex, false)
	if !ok || cred.Key != "sk-codex-test-key" {
		t.Fatalf("stored %+v ok=%v", cred, ok)
	}
}
