package types_test

import (
	"testing"

	"github.com/megascan/open-grok/internal/types"
)

func TestParseProviderAliases(t *testing.T) {
	cases := map[string]types.Provider{
		"grok":        types.ProviderXAI,
		"openai":      types.ProviderCodex,
		"chatgpt":     types.ProviderCodex,
		"moonshot":    types.ProviderKimi,
		"opencode-go": types.ProviderOpenCodeGo,
		"openrouter":  types.ProviderOpenRouter,
		"gemini":      types.ProviderGemini,
	}
	for in, want := range cases {
		got, ok := types.ParseProvider(in)
		if !ok || got != want {
			t.Errorf("ParseProvider(%q) = %q ok=%v, want %q", in, got, ok, want)
		}
	}
}

func TestProviderIdentityNotInferredFromURL(t *testing.T) {
	// Selecting Responses does not select Codex OAuth.
	xai := types.ProviderXAI.Profile()
	if xai.SessionAuth != types.AuthXAISession {
		t.Fatal("xAI must keep its own session auth")
	}
	if !xai.Backends.Supports(types.BackendResponses) {
		t.Fatal("xAI speaks Responses")
	}
	codex := types.ProviderCodex.Profile()
	if codex.SessionAuth != types.AuthCodexOAuth {
		t.Fatal("Codex auth stays Codex OAuth even on Responses")
	}
	if codex.XAIServices != types.XAIServicesDenied {
		t.Fatal("Codex must deny xAI-only exports")
	}
}

func TestChatProvidersStayIsolated(t *testing.T) {
	for _, p := range []types.Provider{
		types.ProviderKimi, types.ProviderFireworks, types.ProviderWafer,
		types.ProviderZAI, types.ProviderRunInfra, types.ProviderGemini,
		types.ProviderOpenRouter,
	} {
		prof := p.Profile()
		if prof.XAIServices != types.XAIServicesDenied {
			t.Errorf("%s must deny xAI exports", p)
		}
		if prof.SessionAuth != types.AuthAPIKeyOnly {
			t.Errorf("%s must be API-key-only", p)
		}
		if prof.HostedToolDialect != types.HostedNone {
			t.Errorf("%s must not advertise hosted tools", p)
		}
		if !prof.Backends.Supports(types.BackendChat) {
			t.Errorf("%s must speak Chat Completions", p)
		}
	}
}

func TestLoginPickerOrder(t *testing.T) {
	want := []string{
		"xai", "codex", "kimi", "fireworks", "deepseek", "meta",
		"opencode_go", "wafer", "zai", "runinfra", "gemini", "openrouter",
	}
	if len(types.AllProviders) != len(want) {
		t.Fatalf("got %d providers, want %d", len(types.AllProviders), len(want))
	}
	for i, p := range types.AllProviders {
		if string(p) != want[i] {
			t.Errorf("index %d: %s want %s", i, p, want[i])
		}
	}
}
