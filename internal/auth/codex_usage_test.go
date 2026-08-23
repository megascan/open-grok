package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/oauth"
)

func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

func TestFetchCodexUsageHitsWhamEndpoints(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	_ = os.Unsetenv("OPENAI_API_KEY")
	_ = os.Unsetenv("CODEX_API_KEY")

	idToken := testJWT(t, map[string]any{
		"email": "dev@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":         "acct_123",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		},
	})
	if err := auth.SaveCodexOAuth(oauth.TokenResponse{
		IDToken: idToken, AccessToken: "access-token", RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatal(err)
	}

	var gotAuth, gotAccount atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotAccount.Store(r.Header.Get("ChatGPT-Account-ID"))
		switch r.URL.Path {
		case "/wham/usage":
			_, _ = w.Write([]byte(`{
				"plan_type": "pro",
				"rate_limit": {
					"allowed": true,
					"limit_reached": false,
					"primary_window": {
						"used_percent": 25,
						"limit_window_seconds": 18000,
						"reset_after_seconds": 100,
						"reset_at": 200
					},
					"secondary_window": {
						"used_percent": 50,
						"limit_window_seconds": 604800,
						"reset_after_seconds": 300,
						"reset_at": 400
					}
				},
				"credits": {"has_credits": true, "unlimited": false, "balance": "12.50"},
				"additional_rate_limits": [{
					"limit_name": "codex_other",
					"metered_feature": null,
					"rate_limit": {
						"allowed": true,
						"limit_reached": false,
						"primary_window": {
							"used_percent": 50,
							"limit_window_seconds": 18000,
							"reset_after_seconds": 3600,
							"reset_at": 0
						}
					}
				}]
			}`))
		case "/wham/profiles/me":
			_, _ = w.Write([]byte(`{"stats":{"lifetime_tokens":1234567}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ENTROPY_CODEX_BACKEND_BASE_URL", srv.URL)
	t.Setenv("OPENGROK_CODEX_BACKEND_BASE_URL", "")
	t.Setenv("GROK_CODEX_BACKEND_BASE_URL", "")

	snap, err := auth.FetchCodexUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth.Load() != "Bearer access-token" {
		t.Fatalf("authorization %v", gotAuth.Load())
	}
	if gotAccount.Load() != "acct_123" {
		t.Fatalf("account header %v", gotAccount.Load())
	}
	if snap.PlanType == nil || *snap.PlanType != "pro" {
		t.Fatalf("plan %+v", snap.PlanType)
	}
	if snap.RateLimit == nil || snap.RateLimit.PrimaryWindow == nil || snap.RateLimit.PrimaryWindow.UsedPercent != 25 {
		t.Fatalf("rate limit %+v", snap.RateLimit)
	}
	if snap.Account == nil || snap.Account.Email == nil || *snap.Account.Email != "dev@example.com" {
		t.Fatalf("account %+v", snap.Account)
	}
	if snap.TokenUsage == nil || snap.TokenUsage.LifetimeTokens == nil || *snap.TokenUsage.LifetimeTokens != 1_234_567 {
		t.Fatalf("token usage %+v", snap.TokenUsage)
	}
	if len(snap.AdditionalRateLimits) != 1 {
		t.Fatalf("additional %+v", snap.AdditionalRateLimits)
	}
}

func TestFetchCodexUsageDisconnectedWithoutTokens(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	_ = os.Unsetenv("OPENAI_API_KEY")
	_ = os.Unsetenv("CODEX_API_KEY")
	_, err := auth.FetchCodexUsage(context.Background())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not connected") {
		t.Fatalf("err=%v", err)
	}
}

func TestFetchCodexUsageNullAdditionalLimits(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if err := auth.SaveCodexOAuth(oauth.TokenResponse{
		IDToken: testJWT(t, map[string]any{"email": "a@b.c"}), AccessToken: "tok", RefreshToken: "ref",
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wham/usage" {
			_, _ = w.Write([]byte(`{"additional_rate_limits": null, "plan_type": "pro"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ENTROPY_CODEX_BACKEND_BASE_URL", srv.URL)
	snap, err := auth.FetchCodexUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.AdditionalRateLimits == nil {
		t.Fatal("null additional_rate_limits must decode to empty slice")
	}
}
