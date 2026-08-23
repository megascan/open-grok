package pager

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
)

// loginPickerLabel is the Rust /login modal display name.
func loginPickerLabel(p types.Provider) string {
	switch p {
	case types.ProviderXAI:
		return "xAI Grok"
	case types.ProviderCodex:
		return "ChatGPT Codex"
	default:
		return p.DisplayName()
	}
}

// loginPickerDetail is the Rust /login modal description, including live
// API-key status for non-OAuth providers.
func loginPickerDetail(p types.Provider) string {
	switch p {
	case types.ProviderXAI:
		return "Sign in with xAI"
	case types.ProviderCodex:
		return "Connect an OpenAI Codex account"
	default:
		return "API key · " + loginSecretStatus(p)
	}
}

func loginSecretStatus(p types.Provider) string {
	for _, name := range auth.EnvKeys(p) {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return "environment override"
		}
	}
	if _, ok := auth.Lookup(p, false); ok {
		return "saved"
	}
	if p == types.ProviderKimi {
		if _, ok := auth.Lookup(p, true); ok {
			return "saved"
		}
	}
	return "not configured"
}

func parseLoginProvider(arg string) (types.Provider, bool) {
	a := strings.ToLower(strings.TrimSpace(arg))
	switch a {
	case "deep-seek", "deepseek-api":
		return types.ProviderDeepSeek, true
	case "meta-ai", "meta_ai", "meta-api":
		return types.ProviderMeta, true
	case "go":
		return types.ProviderOpenCodeGo, true
	case "wafer-ai", "wafer_ai":
		return types.ProviderWafer, true
	case "ai-studio", "aistudio", "google-gemini":
		return types.ProviderGemini, true
	case "open-router", "open_router":
		return types.ProviderOpenRouter, true
	case "run-infra", "run_infra":
		return types.ProviderRunInfra, true
	}
	return types.ParseProvider(a)
}

func (m *model) fillLoginPicker() {
	m.items = nil
	m.loginP = ""
	m.loginKimiCode = false
	for _, p := range types.AllProviders {
		m.items = append(m.items, listItem{
			Key:    string(p),
			Label:  loginPickerLabel(p),
			Detail: loginPickerDetail(p),
		})
	}
	m.cursor = 0
}

// openLogin opens the in-session provider picker overlay when a chat is
// already running, and the full-screen first-run login otherwise.
func (m *model) openLogin(provider string) {
	m.keyBuf = ""
	m.loginKimiCode = false
	stay := m.sess != nil && m.screen == screenChat
	if stay {
		m.overlay = overlayLogin
	} else {
		m.screen = screenLogin
		m.overlay = overlayNone
	}
	if provider != "" {
		p, ok := parseLoginProvider(provider)
		if ok {
			m.loginP = p
			if provider == "kimi-code" || provider == "kimi_code" {
				m.loginKimiCode = true
			}
			return
		}
	}
	m.fillLoginPicker()
}

func (m *model) renderLoginOverlay(w int) string {
	return modalBox.Width(w).Render(m.loginBody())
}

func (m *model) loginBody() string {
	var b strings.Builder
	if m.loginP == "" {
		writeAccent(&b, titleStyle.Render("Choose a provider"))
		writeAccent(&b, "")
		for i, it := range m.items {
			label := it.Label
			if it.Detail != "" {
				label += "  " + it.Detail
			}
			writeAccent(&b, radioRow(optionHotkey(i), label, i == m.cursor))
		}
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("enter select  ·  esc back"))
		return b.String()
	}
	writeAccent(&b, titleStyle.Render(loginPickerLabel(m.loginP)))
	if m.loginP == types.ProviderCodex {
		writeAccent(&b, dimStyle.Render("Codex OAuth needs a browser (entropy login --codex)."))
		writeAccent(&b, dimStyle.Render("Paste an API key here to store OPENAI_API_KEY in codex-auth.json."))
	} else if m.loginP == types.ProviderXAI {
		writeAccent(&b, dimStyle.Render("Sign in with xAI"))
		writeAccent(&b, dimStyle.Render("Paste an API key. It is written owner-only under ~/.entropy."))
	} else {
		writeAccent(&b, dimStyle.Render("Paste an API key. It is written owner-only under ~/.entropy."))
	}
	writeAccent(&b, "")
	shown := strings.Repeat("•", len(m.keyBuf))
	if shown == "" {
		shown = dimStyle.Render("(hidden)")
	}
	writeAccent(&b, "key: "+shown)
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("enter save  ·  esc back  ·  never commit this key"))
	return b.String()
}

func (m *model) handleLoginKey(msg tea.KeyMsg) tea.Cmd {
	inOverlay := m.overlay == overlayLogin
	if m.loginP == "" {
		s := msg.String()
		switch s {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.items) {
				m.cursor++
			}
		case "esc":
			if inOverlay {
				m.overlay = overlayNone
				return nil
			}
			m.screen = screenWelcome
			m.refreshWelcome()
		case "enter":
			if m.cursor < len(m.items) {
				p, ok := parseLoginProvider(m.items[m.cursor].Key)
				if ok {
					m.loginP = p
					m.keyBuf = ""
				}
			}
		default:
			if idx := optionIndexFromKey(s, len(m.items)); idx >= 0 {
				m.cursor = idx
				p, ok := parseLoginProvider(m.items[idx].Key)
				if ok {
					m.loginP = p
					m.keyBuf = ""
				}
			}
		}
		return nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.loginP = ""
		m.keyBuf = ""
		m.fillLoginPicker()
		return nil
	case tea.KeyEnter:
		key := strings.TrimSpace(m.keyBuf)
		if key == "" {
			return nil
		}
		if err := auth.SaveAPIKey(m.loginP, key, m.loginKimiCode); err != nil {
			m.status = err.Error()
			return nil
		}
		m.keyBuf = ""
		label := loginPickerLabel(m.loginP)
		m.status = "saved " + label + " credentials"
		if inOverlay || m.sess != nil {
			m.overlay = overlayNone
			m.screen = screenChat
			m.appendBlock(block{kind: blockSystem, body: "signed in to " + label + " — credentials under ~/.entropy, never ~/.grok"})
			m.refreshVP()
			return nil
		}
		m.screen = screenWelcome
		m.refreshWelcome()
		return nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.keyBuf) > 0 {
			m.keyBuf = m.keyBuf[:len(m.keyBuf)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.keyBuf += string(msg.Runes)
	}
	return nil
}
