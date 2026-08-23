package voice

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DefaultSampleRate is the STT capture rate in Hz.
const DefaultSampleRate = 16000

// DefaultEndpointingMS is the server-side end-of-speech pause in milliseconds.
const DefaultEndpointingMS = 400

// Config is the streaming-STT transport, matching Rust VoiceConfig.
type Config struct {
	APIBase          string
	STTPath          string
	Language         string
	SampleRate       uint32
	EndpointingMS    uint32
	InterimResults   bool
	ClientIdentifier string
	UserAgent        string
	// WSURL, when set, is used as-is (tests inject httptest ws:// URLs).
	WSURL string
	// Bearer is resolved by the pager; tests inject a fake token.
	Bearer string
}

// DefaultConfig returns Rust VoiceConfig::default values.
func DefaultConfig() Config {
	return Config{
		APIBase:        "https://api.x.ai",
		STTPath:        "/v1/stt",
		Language:       DefaultLanguage,
		SampleRate:     DefaultSampleRate,
		EndpointingMS:  DefaultEndpointingMS,
		InterimResults: true,
	}
}

// ConfigFromEnv overlays process env on the Rust defaults.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	for _, key := range []string{"ENTROPY_VOICE_API_BASE", "OPENGROK_VOICE_API_BASE", "GROK_VOICE_API_BASE", "XAI_API_BASE"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			cfg.APIBase = strings.TrimRight(v, "/")
			break
		}
	}
	for _, key := range []string{"ENTROPY_STT_WS_URL", "OPENGROK_STT_WS_URL", "GROK_STT_WS_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			cfg.WSURL = v
			break
		}
	}
	return cfg
}

// STTWSURL builds the streaming-STT WebSocket URL (without query).
// Plaintext http:// and ws:// bases are rejected so the bearer never leaves TLS.
func (c Config) STTWSURL() (string, error) {
	if strings.TrimSpace(c.WSURL) != "" {
		return strings.TrimSpace(c.WSURL), nil
	}
	return wsURL(c.APIBase, c.STTPath)
}

// STTWSURLWithQuery appends the Rust STT query parameters.
func (c Config) STTWSURLWithQuery() (string, error) {
	base, err := c.STTWSURL()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(c.WSURL) != "" {
		return base, nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("bad STT URL: %w", err)
	}
	rate := c.SampleRate
	if rate == 0 {
		rate = DefaultSampleRate
	}
	ep := c.EndpointingMS
	if ep == 0 {
		ep = DefaultEndpointingMS
	}
	q := u.Query()
	q.Set("sample_rate", fmt.Sprintf("%d", rate))
	q.Set("encoding", "pcm")
	q.Set("interim_results", fmt.Sprintf("%t", c.InterimResults))
	q.Set("language", LanguageForAPI(c.Language))
	q.Set("endpointing", fmt.Sprintf("%d", ep))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func stripScheme(value, scheme string) (string, bool) {
	if len(value) < len(scheme) {
		return "", false
	}
	if !strings.EqualFold(value[:len(scheme)], scheme) {
		return "", false
	}
	return value[len(scheme):], true
}

func wsURL(apiBase, path string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if path == "" {
		path = "v1/stt"
	}
	if _, ok := stripScheme(base, "http://"); ok {
		return "", fmt.Errorf("insecure voice api_base %q: voice requires a TLS endpoint (https:// / wss://). Refusing to send the bearer token over a plaintext connection", apiBase)
	}
	if _, ok := stripScheme(base, "ws://"); ok {
		return "", fmt.Errorf("insecure voice api_base %q: voice requires a TLS endpoint (https:// / wss://). Refusing to send the bearer token over a plaintext connection", apiBase)
	}
	rest := base
	if v, ok := stripScheme(base, "https://"); ok {
		rest = v
	} else if v, ok := stripScheme(base, "wss://"); ok {
		rest = v
	}
	if strings.HasSuffix(rest, "/v1") && strings.HasPrefix(path, "v1/") {
		path = strings.TrimPrefix(path, "v1/")
	}
	return "wss://" + rest + "/" + path, nil
}
