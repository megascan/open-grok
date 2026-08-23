package types

// ApiBackend selects the HTTP protocol. It is independent of credentials.
type ApiBackend string

const (
	BackendChat      ApiBackend = "chat_completions"
	BackendResponses ApiBackend = "responses"
	BackendMessages  ApiBackend = "messages"
)

// ResponsesDialect is the provider-native Responses event shape.
type ResponsesDialect string

const (
	DialectNone     ResponsesDialect = ""
	DialectXAI      ResponsesDialect = "xai"
	DialectCodex    ResponsesDialect = "codex"
	DialectDeepSeek ResponsesDialect = "deepseek"
	DialectMeta     ResponsesDialect = "meta"
)

// CodeModeTransport is the provider-native Code Mode exec encoding.
type CodeModeTransport string

const (
	CodeModeUnsupported   CodeModeTransport = "unsupported"
	CodeModeFunction      CodeModeTransport = "function_envelope"
	CodeModeNativeGrammar CodeModeTransport = "native_custom_grammar"
)

// HostedToolDialect is a provider-hosted tool schema, or empty for client tools.
type HostedToolDialect string

const (
	HostedNone   HostedToolDialect = ""
	HostedXAI    HostedToolDialect = "xai"
	HostedOpenAI HostedToolDialect = "openai"
)

// RequestMetadataPolicy controls private HTTP headers.
type RequestMetadataPolicy string

const (
	MetadataXGrok    RequestMetadataPolicy = "x_grok_headers"
	MetadataStandard RequestMetadataPolicy = "standard_headers_only"
)

// SessionAuthKind is the built-in session credential source.
type SessionAuthKind string

const (
	AuthXAISession SessionAuthKind = "xai_session"
	AuthCodexOAuth SessionAuthKind = "codex_oauth"
	AuthAPIKeyOnly SessionAuthKind = "api_key_only"
)

// XAIServicePolicy is the export boundary for xAI-only services.
type XAIServicePolicy string

const (
	XAIServicesAllowed XAIServicePolicy = "allowed"
	XAIServicesDenied  XAIServicePolicy = "denied"
)

// ToolMode is how client-executed tools are exposed to the model.
type ToolMode string

const (
	ToolModeDirect       ToolMode = "direct"
	ToolModeCodeMode     ToolMode = "code_mode"
	ToolModeCodeModeOnly ToolMode = "code_mode_only"
)

// Backends enumerates the HTTP protocols a provider actually implements.
type Backends struct {
	ChatCompletions bool
	Responses       ResponsesDialect
	Messages        bool
}

// Supports reports whether the provider speaks backend.
func (b Backends) Supports(backend ApiBackend) bool {
	switch backend {
	case BackendChat:
		return b.ChatCompletions
	case BackendResponses:
		return b.Responses != DialectNone
	case BackendMessages:
		return b.Messages
	default:
		return false
	}
}

// Profile is the complete behavior policy for one built-in provider.
type Profile struct {
	Provider          Provider
	Backends          Backends
	CodeModeTransport CodeModeTransport
	HostedToolDialect HostedToolDialect
	NativeWebSearch   bool
	RequestMetadata   RequestMetadataPolicy
	SessionAuth       SessionAuthKind
	XAIServices       XAIServicePolicy
}

// Profile returns the built-in policy. Identity never comes from a URL or slug.
func (p Provider) Profile() Profile {
	switch p {
	case ProviderXAI:
		return Profile{
			Provider:          p,
			Backends:          Backends{ChatCompletions: true, Responses: DialectXAI, Messages: true},
			CodeModeTransport: CodeModeFunction,
			HostedToolDialect: HostedXAI,
			NativeWebSearch:   true,
			RequestMetadata:   MetadataXGrok,
			SessionAuth:       AuthXAISession,
			XAIServices:       XAIServicesAllowed,
		}
	case ProviderCodex:
		return Profile{
			Provider:          p,
			Backends:          Backends{Responses: DialectCodex},
			CodeModeTransport: CodeModeNativeGrammar,
			HostedToolDialect: HostedOpenAI,
			NativeWebSearch:   true,
			RequestMetadata:   MetadataStandard,
			SessionAuth:       AuthCodexOAuth,
			XAIServices:       XAIServicesDenied,
		}
	case ProviderDeepSeek:
		return Profile{
			Provider:          p,
			Backends:          Backends{ChatCompletions: true, Responses: DialectDeepSeek},
			CodeModeTransport: CodeModeFunction,
			HostedToolDialect: HostedOpenAI,
			NativeWebSearch:   true,
			RequestMetadata:   MetadataStandard,
			SessionAuth:       AuthAPIKeyOnly,
			XAIServices:       XAIServicesDenied,
		}
	case ProviderMeta:
		return Profile{
			Provider:          p,
			Backends:          Backends{Responses: DialectMeta},
			CodeModeTransport: CodeModeFunction,
			HostedToolDialect: HostedOpenAI,
			NativeWebSearch:   true,
			RequestMetadata:   MetadataStandard,
			SessionAuth:       AuthAPIKeyOnly,
			XAIServices:       XAIServicesDenied,
		}
	case ProviderOpenCodeGo:
		return chatOnly(p, true)
	default:
		return chatOnly(p, false)
	}
}

func chatOnly(p Provider, messages bool) Profile {
	return Profile{
		Provider:          p,
		Backends:          Backends{ChatCompletions: true, Messages: messages},
		CodeModeTransport: CodeModeUnsupported,
		HostedToolDialect: HostedNone,
		NativeWebSearch:   false,
		RequestMetadata:   MetadataStandard,
		SessionAuth:       AuthAPIKeyOnly,
		XAIServices:       XAIServicesDenied,
	}
}
