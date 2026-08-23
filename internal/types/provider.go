// Package types holds provider, backend, and conversation contracts.
//
// Provider identity comes from model metadata (Provider), never from a model
// slug or URL alone. ApiBackend, ProviderProfile, and AuthScheme stay independent.
package types

// Provider is the stable built-in model provider identity.
type Provider string

const (
	ProviderXAI        Provider = "xai"
	ProviderCodex      Provider = "codex"
	ProviderKimi       Provider = "kimi"
	ProviderFireworks  Provider = "fireworks"
	ProviderDeepSeek   Provider = "deepseek"
	ProviderMeta       Provider = "meta"
	ProviderOpenCodeGo Provider = "opencode_go"
	ProviderWafer      Provider = "wafer"
	ProviderZAI        Provider = "zai"
	ProviderRunInfra   Provider = "runinfra"
	ProviderGemini     Provider = "gemini"
	ProviderOpenRouter Provider = "openrouter"
)

// AllProviders is the login-picker order used by the TUI and CLI.
var AllProviders = []Provider{
	ProviderXAI,
	ProviderCodex,
	ProviderKimi,
	ProviderFireworks,
	ProviderDeepSeek,
	ProviderMeta,
	ProviderOpenCodeGo,
	ProviderWafer,
	ProviderZAI,
	ProviderRunInfra,
	ProviderGemini,
	ProviderOpenRouter,
}

// DisplayName is the human-readable provider name.
func (p Provider) DisplayName() string {
	switch p {
	case ProviderXAI:
		return "xAI"
	case ProviderCodex:
		return "OpenAI Codex"
	case ProviderKimi:
		return "Kimi"
	case ProviderFireworks:
		return "Fireworks AI"
	case ProviderDeepSeek:
		return "DeepSeek"
	case ProviderMeta:
		return "Meta API"
	case ProviderOpenCodeGo:
		return "OpenCode Go"
	case ProviderWafer:
		return "Wafer AI"
	case ProviderZAI:
		return "Z AI"
	case ProviderRunInfra:
		return "RunInfra"
	case ProviderGemini:
		return "Google Gemini"
	case ProviderOpenRouter:
		return "OpenRouter"
	default:
		return string(p)
	}
}

// ParseProvider accepts CLI/slash aliases (kimi, opencode-go, openai, …).
func ParseProvider(s string) (Provider, bool) {
	switch s {
	case "xai", "grok":
		return ProviderXAI, true
	case "codex", "openai", "chatgpt":
		return ProviderCodex, true
	case "kimi", "moonshot", "kimi-code", "kimi_code":
		return ProviderKimi, true
	case "fireworks":
		return ProviderFireworks, true
	case "deepseek":
		return ProviderDeepSeek, true
	case "meta":
		return ProviderMeta, true
	case "opencode-go", "opencode_go", "opencode":
		return ProviderOpenCodeGo, true
	case "wafer":
		return ProviderWafer, true
	case "zai", "z-ai", "z_ai":
		return ProviderZAI, true
	case "runinfra":
		return ProviderRunInfra, true
	case "gemini", "google":
		return ProviderGemini, true
	case "openrouter":
		return ProviderOpenRouter, true
	default:
		return "", false
	}
}

// CLIName is the canonical login argument.
func (p Provider) CLIName() string {
	if p == ProviderOpenCodeGo {
		return "opencode-go"
	}
	return string(p)
}

// AllowsXAIServices reports whether session data may be sent to xAI-only services.
func (p Provider) AllowsXAIServices() bool {
	return p.Profile().XAIServices == XAIServicesAllowed
}

// IsCodex is a convenience for export-boundary checks.
func (p Provider) IsCodex() bool { return p == ProviderCodex }
