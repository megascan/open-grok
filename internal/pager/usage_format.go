package pager

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/types"
)

func formatCombinedUsageSummary(xai, codex string) string {
	return "xAI\n" + xai + "\n\nOpenAI Codex\n" + codex
}

func formatXAIUsageError(err string) string {
	if strings.HasPrefix(err, "View current usage at ") {
		return err
	}
	return "Usage unavailable: " + err
}

func formatCodexUsageError(err string) string {
	if strings.Contains(strings.ToLower(err), "not connected") {
		return "Not connected. Run `open-grok login --codex`."
	}
	return "Usage unavailable: " + err
}

func formatCodexAccountHeader(account auth.CodexAccountSummary) string {
	var lines []string
	if account.Email != nil {
		if email := strings.TrimSpace(*account.Email); email != "" {
			lines = append(lines, "Account: "+email)
		}
	}
	if account.PlanType != nil {
		if plan := strings.TrimSpace(*account.PlanType); plan != "" {
			lines = append(lines, "Plan: "+displayPlanType(plan))
		}
	}
	return strings.Join(lines, "\n")
}

func formatCodexUsageSummary(snap auth.CodexUsageSnapshot) string {
	var lines []string

	account := snap.Account
	if account == nil && snap.PlanType != nil && strings.TrimSpace(*snap.PlanType) != "" {
		plan := strings.TrimSpace(*snap.PlanType)
		account = &auth.CodexAccountSummary{PlanType: &plan}
	}
	if account != nil {
		if header := formatCodexAccountHeader(*account); header != "" {
			lines = append(lines, header)
		}
	}

	if snap.RateLimit != nil {
		if snap.RateLimit.PrimaryWindow != nil {
			lines = append(lines, formatCodexWindow(*snap.RateLimit.PrimaryWindow, false))
		}
		if snap.RateLimit.SecondaryWindow != nil {
			lines = append(lines, formatCodexWindow(*snap.RateLimit.SecondaryWindow, true))
		}
	}
	for _, extra := range snap.AdditionalRateLimits {
		if extra.RateLimit == nil {
			continue
		}
		bucket := "Additional"
		if extra.LimitName != nil && strings.TrimSpace(*extra.LimitName) != "" {
			bucket = displayPlanType(*extra.LimitName)
		} else if extra.MeteredFeature != nil && strings.TrimSpace(*extra.MeteredFeature) != "" {
			bucket = displayPlanType(*extra.MeteredFeature)
		}
		if extra.RateLimit.PrimaryWindow != nil {
			lines = append(lines, bucket+" "+formatCodexWindow(*extra.RateLimit.PrimaryWindow, false))
		}
		if extra.RateLimit.SecondaryWindow != nil {
			lines = append(lines, bucket+" "+formatCodexWindow(*extra.RateLimit.SecondaryWindow, true))
		}
	}

	quotaReached := (snap.RateLimit != nil && snap.RateLimit.LimitReached) || rateLimitReachedTypePresent(snap)
	spendReached := snap.SpendControl != nil && snap.SpendControl.Reached
	if quotaReached || spendReached {
		creditsCover := snap.Credits != nil && (snap.Credits.Unlimited || snap.Credits.HasCredits)
		if quotaReached && creditsCover && !spendReached {
			lines = append(lines, "Status: Using credits")
		} else {
			lines = append(lines, "Status: Rate limited")
		}
	}

	if snap.Credits != nil {
		if snap.Credits.Unlimited {
			lines = append(lines, "Credits: Unlimited")
		} else if snap.Credits.HasCredits {
			if bal, ok := formatCreditBalance(snap.Credits.Balance); ok {
				lines = append(lines, "Credits: "+bal+" credits")
			} else {
				lines = append(lines, "Credits: Available")
			}
		}
	}

	if snap.TokenUsage != nil && snap.TokenUsage.LifetimeTokens != nil && *snap.TokenUsage.LifetimeTokens >= 0 {
		lines = append(lines, "Lifetime tokens: "+formatInteger(uint64(*snap.TokenUsage.LifetimeTokens)))
	}

	if len(lines) == 0 {
		return "Usage data unavailable."
	}
	return strings.Join(lines, "\n")
}

func rateLimitReachedTypePresent(snap auth.CodexUsageSnapshot) bool {
	raw := strings.TrimSpace(string(snap.RateLimitReachedType))
	return raw != "" && raw != "null"
}

func formatCodexWindow(window auth.CodexRateLimitWindow, secondary bool) string {
	label := windowLabel(window.LimitWindowSeconds, secondary)
	remaining := math.Min(100, math.Max(0, 100-window.UsedPercent))
	line := fmt.Sprintf("%s: %.0f%% left", label, remaining)
	if window.ResetAfterSeconds > 0 {
		line += " (resets in " + formatUsageDuration(window.ResetAfterSeconds) + ")"
	}
	return line
}

func windowLabel(seconds int64, secondary bool) string {
	const (
		fiveHours = 5 * 60 * 60
		day       = 24 * 60 * 60
		week      = 7 * day
		month     = 30 * day
		tolerance = 5
	)
	approx := func(expected int64) bool {
		tol := expected * tolerance / 100
		return seconds >= expected-tol && seconds <= expected+tol
	}
	switch {
	case approx(fiveHours):
		return "5h limit"
	case approx(day):
		return "Daily limit"
	case approx(week):
		return "Weekly limit"
	case approx(month):
		return "Monthly limit"
	case secondary:
		return "Secondary usage limit"
	default:
		return "Usage limit"
	}
}

func formatUsageDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	days := seconds / 86400
	hours := seconds % 86400 / 3600
	minutes := seconds % 3600 / 60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		if minutes < 1 {
			minutes = 1
		}
		return fmt.Sprintf("%dm", minutes)
	}
}

func formatCreditBalance(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var num float64
	if json.Unmarshal(raw, &num) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &num); err != nil {
			return "", false
		}
	}
	if math.IsInf(num, 0) || math.IsNaN(num) || num <= 0 {
		return "", false
	}
	return fmt.Sprintf("%.0f", num), true
}

func displayPlanType(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		r, size := utf8.DecodeRuneInString(part)
		out = append(out, string(unicode.ToUpper(r))+part[size:])
	}
	return strings.Join(out, " ")
}

func formatInteger(n uint64) string {
	digits := fmt.Sprintf("%d", n)
	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3)
	for i, ch := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func xaiUsageSummary() string {
	st := auth.Status()
	var lines []string
	for _, p := range types.AllProviders {
		if p == types.ProviderCodex {
			continue
		}
		s, ok := st[p]
		if !ok {
			continue
		}
		lines = append(lines, p.DisplayName()+"  ·  "+s)
	}
	body := formatXAIUsageError("Entropy does not proxy xAI Build credits.")
	if len(lines) == 0 {
		return body
	}
	return strings.Join(lines, "\n") + "\n" + body
}
