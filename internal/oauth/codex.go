// Package oauth implements ChatGPT Codex OAuth (browser PKCE + device code).
//
// Tokens are written to ~/.entropy/codex-auth.json in the Codex-rs shape so
// they stay isolated from xAI auth.json. This never uses the xAI AuthManager.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	codexClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexIssuer    = "https://auth.openai.com"
	codexScope     = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	callbackPort   = 1455
	callbackBackup = 1457
	authTimeout    = 30 * time.Second
	deviceTimeout  = 15 * time.Minute
	callbackWait   = 10 * time.Minute
)

// Endpoints can be overridden in tests via ENTROPY_CODEX_ISSUER (OPENGROK_CODEX_ISSUER is an alias).
type Endpoints struct {
	Issuer   string
	ClientID string
}

// DefaultEndpoints is production ChatGPT Codex OAuth.
func DefaultEndpoints() Endpoints {
	issuer := strings.TrimSpace(os.Getenv("ENTROPY_CODEX_ISSUER"))
	if issuer == "" {
		issuer = strings.TrimSpace(os.Getenv("OPENGROK_CODEX_ISSUER"))
	}
	if issuer == "" {
		issuer = codexIssuer
	}
	return Endpoints{Issuer: strings.TrimRight(issuer, "/"), ClientID: codexClientID}
}

// TokenResponse is the /oauth/token payload.
type TokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// DeviceUserCode is the first device-auth response.
type DeviceUserCode struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     int    `json:"-"`
}

type deviceUserCodeJSON struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Usercode     string `json:"usercode"`
	Interval     any    `json:"interval"`
}

// BrowserSession is an in-flight PKCE login waiting on the localhost callback.
type BrowserSession struct {
	AuthURL     string
	RedirectURL string
	ep          Endpoints
	ln          net.Listener
	srv         *http.Server
	codeCh      chan string
	errCh       chan error
	verifier    string
	closeOnce   sync.Once
}

// StartBrowserLogin binds the Codex PKCE callback and returns before waiting
// so a TUI can show the authorize URL on the alternate screen.
func StartBrowserLogin(ep Endpoints) (*BrowserSession, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackPort))
	if err != nil {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackBackup))
		if err != nil {
			return nil, fmt.Errorf("could not bind Codex OAuth callback on ports %d or %d: %w", callbackPort, callbackBackup, err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirect := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	verifier, challenge := pkce()
	state := randomURL(16)
	authURL := authorizeURL(ep, redirect, challenge, state)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("error") != "" {
			http.Error(w, "Entropy Codex login failed", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("codex oauth: %s", q.Get("error_description")):
			default:
			}
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("codex oauth state mismatch"):
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("codex oauth missing code"):
			default:
			}
			return
		}
		fmt.Fprint(w, "<!doctype html><title>Entropy</title><h1>Signed in to OpenAI Codex</h1><p>You can return to Entropy.</p>")
		select {
		case codeCh <- code:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &BrowserSession{
		AuthURL:     authURL,
		RedirectURL: redirect,
		ep:          ep,
		ln:          ln,
		srv:         srv,
		codeCh:      codeCh,
		errCh:       errCh,
		verifier:    verifier,
	}, nil
}

// OpenBrowser launches the system handler for the authorize URL.
func (s *BrowserSession) OpenBrowser() error {
	if s == nil {
		return fmt.Errorf("codex oauth session is nil")
	}
	return openBrowser(s.AuthURL)
}

// Close shuts down the callback server. Safe to call more than once.
func (s *BrowserSession) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if s.srv != nil {
			_ = s.srv.Shutdown(cctx)
		}
		if s.ln != nil {
			_ = s.ln.Close()
		}
	})
}

// Wait blocks until the ChatGPT callback arrives, then exchanges the code.
func (s *BrowserSession) Wait(ctx context.Context) (TokenResponse, error) {
	if s == nil {
		return TokenResponse{}, fmt.Errorf("codex oauth session is nil")
	}
	defer s.Close()
	timer := time.NewTimer(callbackWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return TokenResponse{}, ctx.Err()
	case err := <-s.errCh:
		return TokenResponse{}, err
	case code := <-s.codeCh:
		return ExchangeCode(ctx, s.ep, code, s.RedirectURL, s.verifier)
	case <-timer.C:
		return TokenResponse{}, fmt.Errorf("timed out waiting for the Codex OAuth callback")
	}
}

// BrowserLogin runs the localhost PKCE callback flow.
func BrowserLogin(ctx context.Context, ep Endpoints, announce bool) (TokenResponse, error) {
	sess, err := StartBrowserLogin(ep)
	if err != nil {
		return TokenResponse{}, err
	}
	if announce {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Signing in to OpenAI Codex with ChatGPT...")
		fmt.Fprintln(os.Stderr, "Open this URL if your browser does not open automatically:")
		fmt.Fprintln(os.Stderr, "  "+sess.AuthURL)
	}
	_ = sess.OpenBrowser()
	return sess.Wait(ctx)
}

// DeviceLogin runs the headless ChatGPT device-code flow.
func DeviceLogin(ctx context.Context, ep Endpoints) (TokenResponse, error) {
	device, err := requestDeviceCode(ctx, ep)
	if err != nil {
		return TokenResponse{}, err
	}
	verify := ep.Issuer + "/codex/device"
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Open this URL and sign in to ChatGPT:")
	fmt.Fprintln(os.Stderr, "  "+verify)
	fmt.Fprintln(os.Stderr, "Then enter this one-time code (expires in 15 minutes):")
	fmt.Fprintln(os.Stderr, "  "+device.UserCode)
	fmt.Fprintln(os.Stderr, "Continue only if you started this login in Entropy.")
	_ = openBrowser(verify)

	deadline := time.Now().Add(deviceTimeout)
	interval := device.Interval
	if interval < 1 {
		interval = 1
	}
	for {
		if time.Now().After(deadline) {
			return TokenResponse{}, fmt.Errorf("Codex device-code login timed out after 15 minutes")
		}
		select {
		case <-ctx.Done():
			return TokenResponse{}, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}
		code, verifier, challenge, err := pollDevice(ctx, ep, device)
		if err != nil {
			if isPending(err) {
				continue
			}
			return TokenResponse{}, err
		}
		if strings.TrimSpace(challenge) == "" {
			return TokenResponse{}, fmt.Errorf("Codex device-code response omitted its PKCE challenge")
		}
		redirect := ep.Issuer + "/deviceauth/callback"
		return ExchangeCode(ctx, ep, code, redirect, verifier)
	}
}

// ExchangeCode swaps an authorization code for tokens.
func ExchangeCode(ctx context.Context, ep Endpoints, code, redirect, verifier string) (TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {ep.ClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: authTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("Codex OAuth token exchange returned %s: %s", resp.Status, truncate(string(body), 240))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return TokenResponse{}, err
	}
	if tok.AccessToken == "" {
		return TokenResponse{}, fmt.Errorf("Codex OAuth token response omitted access_token")
	}
	return tok, nil
}

// Refresh swaps a refresh token for a new access token.
func Refresh(ctx context.Context, ep Endpoints, refreshToken string) (TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ep.ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: authTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("Codex OAuth refresh returned %s", resp.Status)
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return TokenResponse{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func requestDeviceCode(ctx context.Context, ep Endpoints) (DeviceUserCode, error) {
	payload, _ := json.Marshal(map[string]string{"client_id": ep.ClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Issuer+"/api/accounts/deviceauth/usercode", strings.NewReader(string(payload)))
	if err != nil {
		return DeviceUserCode{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: authTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return DeviceUserCode{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return DeviceUserCode{}, fmt.Errorf("Codex device-code request returned %s", resp.Status)
	}
	var raw deviceUserCodeJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return DeviceUserCode{}, err
	}
	code := raw.UserCode
	if code == "" {
		code = raw.Usercode
	}
	interval := 2
	switch v := raw.Interval.(type) {
	case float64:
		interval = int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			interval = n
		}
	}
	return DeviceUserCode{DeviceAuthID: raw.DeviceAuthID, UserCode: code, Interval: interval}, nil
}

type pendingErr struct{}

func (pendingErr) Error() string { return "authorization_pending" }

func isPending(err error) bool {
	_, ok := err.(pendingErr)
	return ok
}

func pollDevice(ctx context.Context, ep Endpoints, device DeviceUserCode) (code, verifier, challenge string, err error) {
	payload, _ := json.Marshal(map[string]string{
		"device_auth_id": device.DeviceAuthID,
		"user_code":      device.UserCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Issuer+"/api/accounts/deviceauth/token", strings.NewReader(string(payload)))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: authTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 403 || resp.StatusCode == 404 {
		return "", "", "", pendingErr{}
	}
	if resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("Codex device-code login returned %s", resp.Status)
	}
	var wrap struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeChallenge     string `json:"code_challenge"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return "", "", "", err
	}
	return wrap.AuthorizationCode, wrap.CodeVerifier, wrap.CodeChallenge, nil
}

func authorizeURL(ep Endpoints, redirect, challenge, state string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {ep.ClientID},
		"redirect_uri":               {redirect},
		"scope":                      {codexScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"codex_cli_rs"},
	}
	return ep.Issuer + "/oauth/authorize?" + q.Encode()
}

func pkce() (verifier, challenge string) {
	verifier = randomURL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func randomURL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
