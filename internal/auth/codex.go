package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/oauth"
	"github.com/megascan/open-grok/internal/types"
)

// CodexFile is the Codex-rs / Entropy shared shape of ~/.entropy/codex-auth.json.
type CodexFile struct {
	AuthMode     string       `json:"auth_mode,omitempty"`
	OpenAIAPIKey *string      `json:"OPENAI_API_KEY"`
	Tokens       *CodexTokens `json:"tokens,omitempty"`
	LastRefresh  *time.Time   `json:"last_refresh,omitempty"`
}

// CodexTokens is the ChatGPT OAuth token set.
type CodexTokens struct {
	IDToken      string  `json:"id_token"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	AccountID    *string `json:"account_id,omitempty"`
}

func readCodexFile() (CodexFile, bool) {
	data, err := os.ReadFile(config.CodexAuthJSON())
	if err != nil || len(data) == 0 {
		return CodexFile{}, false
	}
	var f CodexFile
	if json.Unmarshal(data, &f) != nil {
		return CodexFile{}, false
	}
	return f, true
}

func persistCodexFile(f CodexFile) error {
	if err := os.MkdirAll(config.Home(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := config.CodexAuthJSON()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SaveCodexAPIKey writes OPENAI_API_KEY into the Codex-rs-shaped store.
func SaveCodexAPIKey(key string) error {
	key = trimKey(key)
	if key == "" {
		return os.ErrInvalid
	}
	now := time.Now().UTC()
	k := key
	return persistCodexFile(CodexFile{
		AuthMode:     "apikey",
		OpenAIAPIKey: &k,
		LastRefresh:  &now,
	})
}

// SaveCodexOAuth writes ChatGPT OAuth tokens into codex-auth.json.
func SaveCodexOAuth(tok oauth.TokenResponse) error {
	now := time.Now().UTC()
	return persistCodexFile(CodexFile{
		AuthMode: "chatgpt",
		Tokens: &CodexTokens{
			IDToken:      tok.IDToken,
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
		},
		LastRefresh: &now,
	})
}

func lookupCodex() (Credential, bool) {
	if f, ok := readCodexFile(); ok {
		if f.OpenAIAPIKey != nil && trimKey(*f.OpenAIAPIKey) != "" {
			return Credential{Key: trimKey(*f.OpenAIAPIKey), AuthMode: "api_key"}, true
		}
		if f.Tokens != nil && f.Tokens.AccessToken != "" {
			return Credential{Key: f.Tokens.AccessToken, AuthMode: "chatgpt"}, true
		}
	}
	path := config.CodexAuthJSON()
	store, err := readStore(path)
	if err != nil {
		return Credential{}, false
	}
	if c, ok := store["codex::api_key"]; ok && c.Key != "" {
		return c, true
	}
	if c, ok := store["codex::oauth"]; ok && c.Key != "" {
		return c, true
	}
	return Credential{}, false
}

// RefreshCodexIfNeeded rotates a ChatGPT access token when it is stale.
func RefreshCodexIfNeeded(ctx context.Context) error {
	f, ok := readCodexFile()
	if !ok || f.Tokens == nil || f.Tokens.RefreshToken == "" {
		return nil
	}
	if f.LastRefresh != nil && time.Since(*f.LastRefresh) < 4*time.Minute {
		return nil
	}
	tok, err := oauth.Refresh(ctx, oauth.DefaultEndpoints(), f.Tokens.RefreshToken)
	if err != nil {
		return err
	}
	if tok.IDToken == "" {
		tok.IDToken = f.Tokens.IDToken
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = f.Tokens.RefreshToken
	}
	return SaveCodexOAuth(tok)
}

func logoutCodex() error {
	_ = os.Remove(config.CodexAuthJSON())
	return nil
}

func resolveCodexBearer() (string, string, error) {
	for _, name := range EnvKeys(types.ProviderCodex) {
		if v := trimKey(os.Getenv(name)); v != "" {
			return v, "env:" + name, nil
		}
	}
	_ = RefreshCodexIfNeeded(context.Background())
	if c, ok := lookupCodex(); ok {
		return c.Key, "store:codex", nil
	}
	return "", "", fmt.Errorf("not signed in to %s (try `entropy login %s`)", types.ProviderCodex.DisplayName(), types.ProviderCodex.CLIName())
}
