// Package auth stores provider-isolated credentials under ~/.opengrok.
//
// xAI and API-key providers share auth.json with distinct scopes. Codex lives
// in the separate codex-auth.json file. Credentials never cross providers.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

const (
	scopeXAIAPIKey      = "xai::api_key"
	scopeKimiAPIKey     = "kimi::api_key"
	scopeKimiCodeAPIKey = "kimi_code::api_key"
	scopeFireworks      = "fireworks::api_key"
	scopeDeepSeek       = "deepseek::api_key"
	scopeMeta           = "meta::api_key"
	scopeOpenCodeGo     = "opencode_go::api_key"
	scopeWafer          = "wafer::api_key"
	scopeZAI            = "zai::api_key"
	scopeRunInfra       = "runinfra::api_key"
	scopeGemini         = "gemini::api_key"
	scopeOpenRouter     = "openrouter::api_key"
)

// Credential is one auth.json / codex-auth.json principal.
type Credential struct {
	Key        string    `json:"key"`
	AuthMode   string    `json:"auth_mode"`
	CreateTime time.Time `json:"create_time"`
	UserID     string    `json:"user_id,omitempty"`
	Email      *string   `json:"email,omitempty"`
}

// Store is a scoped map of credentials.
type Store map[string]Credential

var (
	mu sync.Mutex
)

// ScopeFor returns the auth.json scope for a provider's API key.
func ScopeFor(p types.Provider, kimiCode bool) string {
	switch p {
	case types.ProviderXAI:
		return scopeXAIAPIKey
	case types.ProviderKimi:
		if kimiCode {
			return scopeKimiCodeAPIKey
		}
		return scopeKimiAPIKey
	case types.ProviderFireworks:
		return scopeFireworks
	case types.ProviderDeepSeek:
		return scopeDeepSeek
	case types.ProviderMeta:
		return scopeMeta
	case types.ProviderOpenCodeGo:
		return scopeOpenCodeGo
	case types.ProviderWafer:
		return scopeWafer
	case types.ProviderZAI:
		return scopeZAI
	case types.ProviderRunInfra:
		return scopeRunInfra
	case types.ProviderGemini:
		return scopeGemini
	case types.ProviderOpenRouter:
		return scopeOpenRouter
	default:
		return string(p) + "::api_key"
	}
}

func pathFor(p types.Provider) string {
	if p == types.ProviderCodex {
		return config.CodexAuthJSON()
	}
	return config.AuthJSON()
}

func readStore(path string) (Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Store{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return Store{}, nil
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s == nil {
		s = Store{}
	}
	return s, nil
}

func writeStore(path string, s Store) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SaveAPIKey writes a provider-local API key. Codex uses the Codex-rs
// OPENAI_API_KEY shape inside codex-auth.json so OAuth and API keys coexist
// with the Rust reference store.
func SaveAPIKey(p types.Provider, key string, kimiCode bool) error {
	key = trimKey(key)
	if key == "" {
		return errors.New("empty API key")
	}
	if p == types.ProviderCodex {
		mu.Lock()
		defer mu.Unlock()
		return SaveCodexAPIKey(key)
	}
	mu.Lock()
	defer mu.Unlock()
	path := pathFor(p)
	store, err := readStore(path)
	if err != nil {
		return err
	}
	scope := ScopeFor(p, kimiCode)
	store[scope] = Credential{
		Key:        key,
		AuthMode:   "api_key",
		CreateTime: time.Now().UTC(),
	}
	return writeStore(path, store)
}

// Lookup returns the stored key for a provider, or empty if unsigned-in.
func Lookup(p types.Provider, kimiCode bool) (Credential, bool) {
	mu.Lock()
	defer mu.Unlock()
	if p == types.ProviderCodex {
		return lookupCodex()
	}
	path := pathFor(p)
	store, err := readStore(path)
	if err != nil {
		return Credential{}, false
	}
	scope := ScopeFor(p, kimiCode)
	c, ok := store[scope]
	return c, ok && c.Key != ""
}

// ResolveBearer prefers an explicit environment key, then the stored credential.
func ResolveBearer(p types.Provider, envKeys []string, kimiCode bool) (string, string, error) {
	if p == types.ProviderCodex {
		return resolveCodexBearer()
	}
	for _, name := range envKeys {
		if v := trimKey(os.Getenv(name)); v != "" {
			return v, "env:" + name, nil
		}
	}
	if c, ok := Lookup(p, kimiCode); ok {
		return c.Key, "store:" + ScopeFor(p, kimiCode), nil
	}
	return "", "", fmt.Errorf("not signed in to %s (try `open-grok login %s`)", p.DisplayName(), p.CLIName())
}

// Logout removes credentials for one provider, both, or all API-key scopes.
func Logout(p types.Provider, all bool) error {
	mu.Lock()
	defer mu.Unlock()
	if all {
		_ = os.Remove(config.AuthJSON())
		_ = os.Remove(config.CodexAuthJSON())
		return nil
	}
	path := pathFor(p)
	store, err := readStore(path)
	if err != nil {
		return err
	}
	if p == types.ProviderCodex {
		return logoutCodex()
	} else if p == types.ProviderKimi {
		delete(store, scopeKimiAPIKey)
		delete(store, scopeKimiCodeAPIKey)
	} else {
		delete(store, ScopeFor(p, false))
	}
	if len(store) == 0 {
		_ = os.Remove(path)
		return nil
	}
	return writeStore(path, store)
}

// Status lists which providers currently have a stored or environment credential.
func Status() map[types.Provider]string {
	out := make(map[types.Provider]string)
	for _, p := range types.AllProviders {
		if _, _, err := ResolveBearer(p, EnvKeys(p), false); err == nil {
			out[p] = "signed in"
			continue
		}
		if p == types.ProviderKimi {
			if _, _, err := ResolveBearer(p, []string{"KIMI_CODE_API_KEY"}, true); err == nil {
				out[p] = "signed in (kimi code)"
			}
		}
	}
	return out
}

func trimKey(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// EnvKeys are the process-level BYOK variables accepted for a provider.
func EnvKeys(p types.Provider) []string {
	switch p {
	case types.ProviderXAI:
		return []string{"XAI_API_KEY", "GROK_API_KEY"}
	case types.ProviderCodex:
		return []string{"OPENAI_API_KEY", "CODEX_API_KEY"}
	case types.ProviderKimi:
		return []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"}
	case types.ProviderFireworks:
		return []string{"FIREWORKS_API_KEY"}
	case types.ProviderDeepSeek:
		return []string{"DEEPSEEK_API_KEY"}
	case types.ProviderMeta:
		return []string{"META_API_KEY"}
	case types.ProviderOpenCodeGo:
		return []string{"OPENCODE_API_KEY"}
	case types.ProviderWafer:
		return []string{"WAFER_API_KEY"}
	case types.ProviderZAI:
		return []string{"ZAI_API_KEY"}
	case types.ProviderRunInfra:
		return []string{"RUNINFRA_GATEWAY_KEY", "RUNINFRA_API_KEY"}
	case types.ProviderGemini:
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case types.ProviderOpenRouter:
		return []string{"OPENROUTER_API_KEY"}
	default:
		return nil
	}
}
