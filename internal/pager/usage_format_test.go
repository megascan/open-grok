package pager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/auth"
)

func ptr[T any](v T) *T { return &v }

func testCodexSnapshot(limitReached, hasCredits bool) auth.CodexUsageSnapshot {
	balance, _ := json.Marshal(12.6)
	return auth.CodexUsageSnapshot{
		Account: &auth.CodexAccountSummary{
			Email:    ptr("dev@example.com"),
			PlanType: ptr("pro"),
		},
		RateLimit: &auth.CodexRateLimit{
			Allowed:      true,
			LimitReached: limitReached,
			PrimaryWindow: &auth.CodexRateLimitWindow{
				UsedPercent:        28,
				LimitWindowSeconds: 5 * 60 * 60,
				ResetAfterSeconds:  2*60*60 + 15*60,
			},
			SecondaryWindow: &auth.CodexRateLimitWindow{
				UsedPercent:        59,
				LimitWindowSeconds: 7 * 24 * 60 * 60,
				ResetAfterSeconds:  2 * 24 * 60 * 60,
			},
		},
		Credits: &auth.CodexCredits{
			HasCredits: hasCredits,
			Unlimited:  false,
			Balance:    balance,
		},
		AdditionalRateLimits: []auth.CodexAdditionalRateLimit{{
			LimitName: ptr("codex_other"),
			RateLimit: &auth.CodexRateLimit{
				Allowed: true,
				PrimaryWindow: &auth.CodexRateLimitWindow{
					UsedPercent:        50,
					LimitWindowSeconds: 5 * 60 * 60,
					ResetAfterSeconds:  60 * 60,
				},
			},
		}},
		TokenUsage: &auth.CodexTokenUsageStats{LifetimeTokens: ptr(int64(1_234_567))},
	}
}

func TestCombinedUsageLabelsBothProviders(t *testing.T) {
	got := formatCombinedUsageSummary("Weekly limit: 20%", "5h limit: 80% left")
	want := "xAI\nWeekly limit: 20%\n\nOpenAI Codex\n5h limit: 80% left"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCodexUsageShowsRemainingWindowsCreditsAndAdditionalLimits(t *testing.T) {
	summary := formatCodexUsageSummary(testCodexSnapshot(false, true))
	for _, needle := range []string{
		"Account: dev@example.com",
		"Plan: Pro",
		"5h limit: 72% left",
		"Weekly limit: 41% left",
		"Codex Other 5h limit: 50% left",
		"Credits: 13 credits",
		"Lifetime tokens: 1,234,567",
	} {
		if !strings.Contains(summary, needle) {
			t.Fatalf("missing %q in\n%s", needle, summary)
		}
	}
}

func TestExhaustedQuotaDistinguishesCreditsFromRateLimit(t *testing.T) {
	using := formatCodexUsageSummary(testCodexSnapshot(true, true))
	if !strings.Contains(using, "Status: Using credits") || strings.Contains(using, "Status: Rate limited") {
		t.Fatalf("using credits:\n%s", using)
	}
	limited := formatCodexUsageSummary(testCodexSnapshot(true, false))
	if !strings.Contains(limited, "Status: Rate limited") {
		t.Fatalf("rate limited:\n%s", limited)
	}
}

func TestDisconnectedCodexUsageIsExplicit(t *testing.T) {
	got := formatCodexUsageError("Not connected; run `open-grok login --codex`")
	if got != "Not connected. Run `open-grok login --codex`." {
		t.Fatalf("got %q", got)
	}
}

func TestUsageLimitTabShowsLoadingThenFetchedSummary(t *testing.T) {
	m := testModel(t)
	prev := fetchCodexUsage
	t.Cleanup(func() { fetchCodexUsage = prev })
	fetchCodexUsage = func(context.Context) (auth.CodexUsageSnapshot, error) {
		return testCodexSnapshot(false, true), nil
	}
	cmd := m.openUsage(1)
	if !m.usageLoading {
		t.Fatal("expected loading")
	}
	body := stripANSI(m.renderUsage(72))
	if !strings.Contains(body, "Loading usage") {
		t.Fatalf("loading:\n%s", body)
	}
	if cmd == nil {
		t.Fatal("expected fetch command")
	}
	msg, ok := cmd().(usageFetchedMsg)
	if !ok || msg.text == "" {
		t.Fatalf("msg %+v ok=%v", msg, ok)
	}
	m.usageLoading = false
	m.usageLimitText = msg.text
	body = stripANSI(m.renderUsage(72))
	if !strings.Contains(body, "OpenAI Codex") || !strings.Contains(body, "5h limit: 72% left") {
		t.Fatalf("fetched:\n%s", body)
	}
	if !strings.Contains(body, "xAI") {
		t.Fatalf("missing xAI section:\n%s", body)
	}
}
