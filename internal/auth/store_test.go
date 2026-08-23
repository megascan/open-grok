package auth_test

import (
	"os"
	"testing"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

func TestProviderKeysStayIsolated(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	for _, e := range []string{"FIREWORKS_API_KEY", "XAI_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(e, "")
		_ = os.Unsetenv(e)
	}

	if err := auth.SaveAPIKey(types.ProviderFireworks, "fw-key", false); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveAPIKey(types.ProviderXAI, "xai-key", false); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveAPIKey(types.ProviderCodex, "codex-key", false); err != nil {
		t.Fatal(err)
	}

	fw, _, err := auth.ResolveBearer(types.ProviderFireworks, auth.EnvKeys(types.ProviderFireworks), false)
	if err != nil || fw != "fw-key" {
		t.Fatalf("fireworks = %q err=%v", fw, err)
	}
	xai, _, err := auth.ResolveBearer(types.ProviderXAI, nil, false)
	if err != nil || xai != "xai-key" {
		t.Fatalf("xai = %q err=%v", xai, err)
	}
	codex, _, err := auth.ResolveBearer(types.ProviderCodex, nil, false)
	if err != nil || codex != "codex-key" {
		t.Fatalf("codex = %q err=%v", codex, err)
	}
	if _, _, err := auth.ResolveBearer(types.ProviderOpenRouter, nil, false); err == nil {
		t.Fatal("openrouter must not inherit another provider's key")
	}

	if err := auth.Logout(types.ProviderFireworks, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.ResolveBearer(types.ProviderFireworks, nil, false); err == nil {
		t.Fatal("logout fireworks must not leave a key")
	}
	if _, _, err := auth.ResolveBearer(types.ProviderXAI, nil, false); err != nil {
		t.Fatal("xAI key must survive fireworks logout")
	}
}

func TestKimiPlatformAndCodeScopesAreDistinct(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if err := auth.SaveAPIKey(types.ProviderKimi, "platform", false); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveAPIKey(types.ProviderKimi, "code", true); err != nil {
		t.Fatal(err)
	}
	p, _, err := auth.ResolveBearer(types.ProviderKimi, nil, false)
	if err != nil || p != "platform" {
		t.Fatalf("platform = %q err=%v", p, err)
	}
	c, _, err := auth.ResolveBearer(types.ProviderKimi, nil, true)
	if err != nil || c != "code" {
		t.Fatalf("code = %q err=%v", c, err)
	}
}

func TestEnvKeyWinsOverStore(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if err := auth.SaveAPIKey(types.ProviderGemini, "stored", false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_API_KEY", "from-env")
	got, src, err := auth.ResolveBearer(types.ProviderGemini, auth.EnvKeys(types.ProviderGemini), false)
	if err != nil || got != "from-env" {
		t.Fatalf("got %q src=%s err=%v", got, src, err)
	}
}
