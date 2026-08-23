// Package catalog loads curated and live model metadata.
//
// Provider identity is taken from each model's provider field, never guessed
// from the model slug or base URL.
package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

//go:embed default_models.json
var defaultModelsJSON []byte

// Model is one picker / wire entry.
type Model struct {
	ID              string           `json:"id"`
	Model           string           `json:"model"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	BaseURL         string           `json:"base_url"`
	ContextWindow   int              `json:"context_window"`
	APIBackend      types.ApiBackend `json:"api_backend"`
	Provider        types.Provider   `json:"provider"`
	EnvKey          string           `json:"env_key"`
	ToolMode        types.ToolMode   `json:"tool_mode"`
	SupportedInAPI  *bool            `json:"supported_in_api"`
	NativeSearch    bool             `json:"supports_backend_search"`
	ReasoningEffort string           `json:"reasoning_effort"`
	// SupportsTools is nil when unknown: :free ids default off, others on.
	SupportsTools *bool `json:"supports_tools,omitempty"`
}

type file struct {
	Default string  `json:"default"`
	Models  []Model `json:"models"`
}

var defaults file

func init() {
	if err := json.Unmarshal(defaultModelsJSON, &defaults); err != nil {
		panic("default_models.json: " + err.Error())
	}
	for i := range defaults.Models {
		m := &defaults.Models[i]
		if m.ID == "" {
			m.ID = m.Model
		}
		if m.Provider == "" {
			m.Provider = types.ProviderXAI
		}
		if m.APIBackend == "" {
			m.APIBackend = types.BackendChat
		}
		if m.ToolMode == "" {
			m.ToolMode = types.ToolModeDirect
		}
		if m.BaseURL == "" {
			m.BaseURL = DefaultBaseURL(m.Provider)
		}
		if m.EnvKey == "" {
			m.EnvKey = defaultEnvKey(m.Provider)
		}
	}
	defaults.Models = append(defaults.Models, extraCurated()...)
}

// DefaultID is the configured default model slug.
func DefaultID() string {
	if defaults.Default != "" {
		return defaults.Default
	}
	return "grok-4.6"
}

// All returns the static catalog (curated + embedded defaults).
func All() []Model {
	out := make([]Model, len(defaults.Models))
	copy(out, defaults.Models)
	return out
}

// Lookup finds a model by id or wire name (case-sensitive, then case-fold).
func Lookup(id string) (Model, bool) {
	return LookupIn(id, nil)
}

// LookupIn searches the static catalog plus an optional live overlay.
func LookupIn(id string, extra []Model) (Model, bool) {
	if id == "" {
		id = DefaultID()
	}
	pool := All()
	pool = append(pool, extra...)
	for _, m := range pool {
		if m.ID == id || m.Model == id {
			return m, true
		}
	}
	fold := strings.ToLower(id)
	for _, m := range pool {
		if strings.ToLower(m.ID) == fold || strings.ToLower(m.Model) == fold {
			return m, true
		}
	}
	return Model{}, false
}

// ToolsEnabled reports whether client tools should be advertised.
// Identity still comes from model metadata; :free is a catalog property, not a guess from a URL.
func (m Model) ToolsEnabled() bool {
	if m.SupportsTools != nil {
		return *m.SupportsTools
	}
	id := strings.ToLower(m.ID + " " + m.Model)
	return !strings.Contains(id, ":free")
}

// DefaultBaseURL is the official origin for a provider. URLs never imply identity.
func DefaultBaseURL(p types.Provider) string {
	switch p {
	case types.ProviderXAI:
		return "https://api.x.ai/v1"
	case types.ProviderCodex:
		return "https://chatgpt.com/backend-api/codex"
	case types.ProviderKimi:
		return "https://api.moonshot.ai/v1"
	case types.ProviderFireworks:
		return "https://api.fireworks.ai/inference/v1"
	case types.ProviderDeepSeek:
		return "https://api.deepseek.com"
	case types.ProviderMeta:
		return "https://api.meta.ai/v1"
	case types.ProviderOpenCodeGo:
		return "https://opencode.ai/zen/go/v1"
	case types.ProviderWafer:
		return "https://pass.wafer.ai/v1"
	case types.ProviderZAI:
		return "https://api.z.ai/api/coding/paas/v4"
	case types.ProviderRunInfra:
		return "https://api.runinfra.ai/v1"
	case types.ProviderGemini:
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case types.ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	default:
		return ""
	}
}

// TrustedHost reports whether baseURL is on the provider's official origin.
func TrustedHost(p types.Provider, baseURL string) bool {
	want := DefaultBaseURL(p)
	if want == "" || baseURL == "" {
		return false
	}
	return strings.HasPrefix(strings.TrimRight(baseURL, "/"), strings.TrimRight(want, "/")) ||
		hostOf(baseURL) == hostOf(want)
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func extraCurated() []Model {
	mk := func(p types.Provider, id, name, desc string) Model {
		return Model{
			ID:            id,
			Model:         id,
			Name:          name,
			Description:   desc,
			BaseURL:       DefaultBaseURL(p),
			ContextWindow: 128000,
			APIBackend:    types.BackendChat,
			Provider:      p,
			EnvKey:        defaultEnvKey(p),
			ToolMode:      types.ToolModeDirect,
		}
	}
	toolsOff := false
	free := func(id, name, desc string) Model {
		m := mk(types.ProviderOpenRouter, id, name, desc)
		m.SupportsTools = &toolsOff
		return m
	}
	return []Model{
		mk(types.ProviderGemini, "gemini-3.7-flash", "Gemini 3.7 Flash", "Gemini 3.7 Flash on Google AI Studio"),
		mk(types.ProviderGemini, "gemini-3.6-flash", "Gemini 3.6 Flash", "Gemini 3.6 Flash on Google AI Studio"),
		mk(types.ProviderGemini, "gemini-3.5-flash-lite", "Gemini 3.5 Flash-Lite", "Gemini 3.5 Flash-Lite on Google AI Studio"),
		mk(types.ProviderGemini, "gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", "Gemini 3.1 Pro Preview on Google AI Studio"),
		mk(types.ProviderZAI, "glm-5.3", "GLM 5.3", "Z AI GLM 5.3 (Coding Plan)"),
		mk(types.ProviderZAI, "glm-5.2", "GLM 5.2", "Z AI GLM 5.2 (Coding Plan)"),
		mk(types.ProviderWafer, "wafer-default", "Wafer AI", "Wafer AI Chat Completions (live /models)"),
		mk(types.ProviderRunInfra, "deepseek-v4-flash", "DeepSeek V4 Flash", "RunInfra hosted DeepSeek V4 Flash"),
		mk(types.ProviderOpenRouter, "openrouter/auto", "OpenRouter Auto", "OpenRouter Chat Completions gateway"),
		free("openrouter/free", "OpenRouter Free", "OpenRouter free-model router"),
		free("nvidia/nemotron-nano-9b-v2:free", "Nemotron Nano 9B (free)", "OpenRouter free NVIDIA Nemotron Nano 9B"),
		free("qwen/qwen3-4b:free", "Qwen3 4B (free)", "OpenRouter free Qwen3 4B"),
		free("meta-llama/llama-3.3-8b-instruct:free", "Llama 3.3 8B (free)", "OpenRouter free Llama 3.3 8B Instruct"),
		mk(types.ProviderOpenCodeGo, "opencode-go", "OpenCode Go", "OpenCode Go Chat or Messages"),
	}
}

func defaultEnvKey(p types.Provider) string {
	switch p {
	case types.ProviderOpenRouter:
		return "OPENROUTER_API_KEY"
	case types.ProviderXAI:
		return "XAI_API_KEY"
	case types.ProviderCodex:
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}
