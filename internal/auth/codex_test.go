package auth_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/oauth"
	"github.com/megascan/open-grok/internal/types"
)

func TestCodexOAuthStoreIsRustShaped(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	_ = os.Unsetenv("OPENAI_API_KEY")
	_ = os.Unsetenv("CODEX_API_KEY")
	if err := auth.SaveCodexOAuth(oauth.TokenResponse{
		IDToken: "id", AccessToken: "tok", RefreshToken: "ref",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(config.CodexAuthJSON())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		t.Fatal(string(data))
	}
	if raw["auth_mode"] != "chatgpt" {
		t.Fatalf("auth_mode %v", raw["auth_mode"])
	}
	tokens, _ := raw["tokens"].(map[string]any)
	if tokens["access_token"] != "tok" {
		t.Fatalf("tokens %v", tokens)
	}
	got, src, err := auth.ResolveBearer(types.ProviderCodex, nil, false)
	if err != nil || got != "tok" {
		t.Fatalf("got %q src=%s err=%v", got, src, err)
	}
}
