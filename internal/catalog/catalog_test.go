package catalog_test

import (
	"testing"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

func TestDefaultModelIsXAIMetadata(t *testing.T) {
	m, ok := catalog.Lookup(catalog.DefaultID())
	if !ok {
		t.Fatal("missing default")
	}
	if m.Provider != types.ProviderXAI {
		t.Fatalf("default provider %s", m.Provider)
	}
	if m.APIBackend != types.BackendResponses {
		t.Fatalf("default backend %s", m.APIBackend)
	}
}

func TestProviderNotGuessedFromURL(t *testing.T) {
	m, ok := catalog.Lookup("kimi-k3")
	if !ok {
		t.Fatal("missing kimi-k3")
	}
	if m.Provider != types.ProviderKimi {
		t.Fatal("kimi-k3 must be Kimi even though the URL is OpenAI-compatible")
	}
	if !catalog.TrustedHost(types.ProviderKimi, m.BaseURL) {
		t.Fatalf("unexpected base %s", m.BaseURL)
	}
}

func TestCodexModelsStayOnCodexStore(t *testing.T) {
	m, ok := catalog.Lookup("gpt-5.6-sol")
	if !ok {
		t.Fatal("missing sol")
	}
	if m.Provider != types.ProviderCodex {
		t.Fatal("sol is a Codex model")
	}
	if m.Provider.Profile().SessionAuth != types.AuthCodexOAuth {
		t.Fatal("Codex session auth is OAuth, independent of Responses")
	}
}
