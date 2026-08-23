package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	codexBackendBaseURL      = "https://chatgpt.com/backend-api"
	codexUsageRequestTimeout = 30 * time.Second
	codexNotConnectedErr     = "Not connected; run `open-grok login --codex`"
)

// CodexAccountSummary is the signed-in ChatGPT identity carried on a usage snapshot.
type CodexAccountSummary struct {
	Email     *string
	AccountID *string
	PlanType  *string
}

// CodexUsageSnapshot is the rust CodexUsageSnapshot wire shape plus identity extras.
type CodexUsageSnapshot struct {
	Account              *CodexAccountSummary       `json:"-"`
	PlanType             *string                    `json:"plan_type"`
	RateLimit            *CodexRateLimit            `json:"rate_limit"`
	Credits              *CodexCredits              `json:"credits"`
	SpendControl         *CodexSpendControl         `json:"spend_control"`
	AdditionalRateLimits []CodexAdditionalRateLimit `json:"additional_rate_limits"`
	RateLimitReachedType json.RawMessage            `json:"rate_limit_reached_type"`
	TokenUsage           *CodexTokenUsageStats      `json:"-"`
	FetchedAt            time.Time                  `json:"-"`
}

// CodexRateLimit is one ChatGPT quota bucket pair.
type CodexRateLimit struct {
	Allowed         bool                  `json:"allowed"`
	LimitReached    bool                  `json:"limit_reached"`
	PrimaryWindow   *CodexRateLimitWindow `json:"primary_window"`
	SecondaryWindow *CodexRateLimitWindow `json:"secondary_window"`
}

// CodexRateLimitWindow is a rolling usage window.
type CodexRateLimitWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// CodexCredits is prepaid ChatGPT credit state.
type CodexCredits struct {
	HasCredits bool            `json:"has_credits"`
	Unlimited  bool            `json:"unlimited"`
	Balance    json.RawMessage `json:"balance"`
}

// CodexSpendControl is an account spend cap.
type CodexSpendControl struct {
	Reached         bool            `json:"reached"`
	IndividualLimit json.RawMessage `json:"individual_limit"`
}

// CodexAdditionalRateLimit is a named extra meter (review, Codex Other, …).
type CodexAdditionalRateLimit struct {
	LimitName      *string         `json:"limit_name"`
	MeteredFeature *string         `json:"metered_feature"`
	RateLimit      *CodexRateLimit `json:"rate_limit"`
}

// CodexTokenUsageStats is the /wham/profiles/me stats object.
type CodexTokenUsageStats struct {
	LifetimeTokens        *int64 `json:"lifetime_tokens"`
	PeakDailyTokens       *int64 `json:"peak_daily_tokens"`
	LongestRunningTurnSec *int64 `json:"longest_running_turn_sec"`
	CurrentStreakDays     *int64 `json:"current_streak_days"`
	LongestStreakDays     *int64 `json:"longest_streak_days"`
}

type tokenUsageProfile struct {
	Stats CodexTokenUsageStats `json:"stats"`
}

type jwtAuthClaims struct {
	ChatGPTAccountID        string `json:"chatgpt_account_id"`
	ChatGPTPlanType         string `json:"chatgpt_plan_type"`
	ChatGPTUserID           string `json:"chatgpt_user_id"`
	UserID                  string `json:"user_id"`
	ChatGPTAccountIsFedramp bool   `json:"chatgpt_account_is_fedramp"`
}

type jwtPayload struct {
	Email   string          `json:"email"`
	Auth    *jwtAuthClaims  `json:"https://api.openai.com/auth"`
	Profile json.RawMessage `json:"https://api.openai.com/profile"`
}

type jwtProfile struct {
	Email string `json:"email"`
}

type codexCredentials struct {
	AccessToken      string
	AccountID        string
	Email            string
	PlanType         string
	AccountIsFedramp bool
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string { return "OpenAI Codex rejected the OAuth token" }

func isUnauthorized(err error) bool {
	_, ok := err.(unauthorizedError)
	return ok
}

// CodexBackendBaseURL is chatgpt.com/backend-api, overridable for hermetic tests.
func CodexBackendBaseURL() string {
	for _, name := range []string{
		"ENTROPY_CODEX_BACKEND_BASE_URL",
		"OPENGROK_CODEX_BACKEND_BASE_URL",
		"GROK_CODEX_BACKEND_BASE_URL",
	} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return strings.TrimRight(codexBackendBaseURL, "/")
}

// CodexAccountFromStore returns identity parsed from the on-disk ChatGPT tokens.
func CodexAccountFromStore() *CodexAccountSummary {
	creds, ok := loadCodexCredentials()
	if !ok {
		return nil
	}
	sum := accountSummaryFromCreds(creds)
	if sum.Email == nil && sum.AccountID == nil && sum.PlanType == nil {
		return nil
	}
	return &sum
}

func loadCodexCredentials() (codexCredentials, bool) {
	f, ok := readCodexFile()
	if !ok || f.Tokens == nil || strings.TrimSpace(f.Tokens.AccessToken) == "" {
		return codexCredentials{}, false
	}
	return credentialsFromTokens(f.Tokens), true
}

func credentialsFromTokens(tokens *CodexTokens) codexCredentials {
	claims := decodeJWTPayload(tokens.IDToken)
	email, plan, accountID := "", "", ""
	fedramp := false
	if claims != nil {
		email = strings.TrimSpace(claims.Email)
		if email == "" && len(claims.Profile) > 0 {
			var profile jwtProfile
			if json.Unmarshal(claims.Profile, &profile) == nil {
				email = strings.TrimSpace(profile.Email)
			}
		}
		if claims.Auth != nil {
			plan = strings.TrimSpace(claims.Auth.ChatGPTPlanType)
			fedramp = claims.Auth.ChatGPTAccountIsFedramp
			accountID = strings.TrimSpace(claims.Auth.ChatGPTAccountID)
		}
	}
	if tokens.AccountID != nil && strings.TrimSpace(*tokens.AccountID) != "" {
		accountID = strings.TrimSpace(*tokens.AccountID)
	}
	return codexCredentials{
		AccessToken:      tokens.AccessToken,
		AccountID:        accountID,
		Email:            email,
		PlanType:         plan,
		AccountIsFedramp: fedramp,
	}
}

func accountSummaryFromCreds(c codexCredentials) CodexAccountSummary {
	var sum CodexAccountSummary
	if e := strings.TrimSpace(c.Email); e != "" {
		sum.Email = &e
	}
	if id := strings.TrimSpace(c.AccountID); id != "" {
		sum.AccountID = &id
	}
	if p := strings.TrimSpace(c.PlanType); p != "" {
		sum.PlanType = &p
	}
	return sum
}

func accountIDFromIDToken(idToken string) string {
	claims := decodeJWTPayload(idToken)
	if claims == nil || claims.Auth == nil {
		return ""
	}
	return strings.TrimSpace(claims.Auth.ChatGPTAccountID)
}

func decodeJWTPayload(token string) *jwtPayload {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		padded := parts[1]
		switch len(padded) % 4 {
		case 2:
			padded += "=="
		case 3:
			padded += "="
		}
		raw, err = base64.URLEncoding.DecodeString(padded)
		if err != nil {
			return nil
		}
	}
	var claims jwtPayload
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return &claims
}

// FetchCodexUsage GETs ChatGPT /wham/usage and /wham/profiles/me with the stored OAuth bearer.
func FetchCodexUsage(ctx context.Context) (CodexUsageSnapshot, error) {
	creds, ok := loadCodexCredentials()
	if !ok {
		return CodexUsageSnapshot{}, fmt.Errorf("%s", codexNotConnectedErr)
	}
	snap, err := fetchCodexUsageWithCredentials(ctx, creds)
	if err == nil {
		return snap, nil
	}
	if !isUnauthorized(err) {
		return CodexUsageSnapshot{}, err
	}
	if refreshErr := RefreshCodex(ctx); refreshErr != nil {
		return CodexUsageSnapshot{}, fmt.Errorf("%s", codexNotConnectedErr)
	}
	creds, ok = loadCodexCredentials()
	if !ok {
		return CodexUsageSnapshot{}, fmt.Errorf("%s", codexNotConnectedErr)
	}
	return fetchCodexUsageWithCredentials(ctx, creds)
}

func fetchCodexUsageWithCredentials(ctx context.Context, creds codexCredentials) (CodexUsageSnapshot, error) {
	base := CodexBackendBaseURL()
	client := &http.Client{Timeout: codexUsageRequestTimeout}

	type result struct {
		status int
		body   []byte
		err    error
	}
	usageCh := make(chan result, 1)
	profileCh := make(chan result, 1)
	go func() {
		status, body, err := doCodexGET(ctx, client, base+"/wham/usage", creds)
		usageCh <- result{status, body, err}
	}()
	go func() {
		status, body, err := doCodexGET(ctx, client, base+"/wham/profiles/me", creds)
		profileCh <- result{status, body, err}
	}()

	usageRes := <-usageCh
	profileRes := <-profileCh
	if usageRes.err != nil {
		return CodexUsageSnapshot{}, fmt.Errorf("Codex usage request failed: %w", usageRes.err)
	}
	if usageRes.status == http.StatusUnauthorized {
		return CodexUsageSnapshot{}, unauthorizedError{}
	}
	if usageRes.status < 200 || usageRes.status >= 300 {
		excerpt := strings.ReplaceAll(strings.ReplaceAll(string(usageRes.body), "\n", " "), "\r", " ")
		if len(excerpt) > 512 {
			excerpt = excerpt[:512] + "…"
		}
		return CodexUsageSnapshot{}, fmt.Errorf("Codex usage request returned %d: %s", usageRes.status, excerpt)
	}
	var snap CodexUsageSnapshot
	if err := json.Unmarshal(usageRes.body, &snap); err != nil {
		return CodexUsageSnapshot{}, fmt.Errorf("Codex usage response was invalid: %w", err)
	}
	if snap.AdditionalRateLimits == nil {
		snap.AdditionalRateLimits = []CodexAdditionalRateLimit{}
	}
	sum := accountSummaryFromCreds(creds)
	if snap.PlanType != nil && strings.TrimSpace(*snap.PlanType) != "" && sum.PlanType == nil {
		p := strings.TrimSpace(*snap.PlanType)
		sum.PlanType = &p
	}
	if sum.Email != nil || sum.AccountID != nil || sum.PlanType != nil {
		snap.Account = &sum
	}
	snap.FetchedAt = time.Now().UTC()
	if profileRes.err == nil && profileRes.status >= 200 && profileRes.status < 300 {
		var profile tokenUsageProfile
		if json.Unmarshal(profileRes.body, &profile) == nil {
			stats := profile.Stats
			snap.TokenUsage = &stats
		}
	}
	return snap, nil
}

func doCodexGET(ctx context.Context, client *http.Client, rawURL string, creds codexCredentials) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	if creds.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", creds.AccountID)
	}
	if creds.AccountIsFedramp {
		req.Header.Set("X-OpenAI-Fedramp", "true")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

// RefreshCodex rotates the ChatGPT access token even if it was recently refreshed.
func RefreshCodex(ctx context.Context) error {
	return refreshCodex(ctx, true)
}
