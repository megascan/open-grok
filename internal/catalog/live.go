package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// LiveClient discovers models from a provider /models endpoint.
type LiveClient struct {
	HTTP *http.Client
}

func (c *LiveClient) client() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

type modelsList struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextWindow int    `json:"context_window"`
		ContextLength int    `json:"context_length"`
	} `json:"data"`
}

// FetchOpenAIModels GETs {base}/models with a bearer token and maps ids onto
// the given provider. The provider argument is authoritative; the URL is not.
func (c *LiveClient) FetchOpenAIModels(p types.Provider, baseURL, bearer string) ([]Model, error) {
	if p == "" {
		return nil, fmt.Errorf("provider identity is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	req, err := http.NewRequest(http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if p == types.ProviderOpenRouter {
		req.Header.Set("HTTP-Referer", "https://github.com/megascan/open-grok")
		req.Header.Set("X-Title", "Entropy")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s /models: HTTP %d", p, resp.StatusCode)
	}
	if p == types.ProviderOpenRouter {
		return parseOpenRouterModels(baseURL, body)
	}
	var list modelsList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(list.Data))
	for _, d := range list.Data {
		if d.ID == "" {
			continue
		}
		ctx := d.ContextWindow
		if ctx == 0 {
			ctx = d.ContextLength
		}
		name := d.Name
		if name == "" {
			name = d.ID
		}
		out = append(out, Model{
			ID:            d.ID,
			Model:         d.ID,
			Name:          name,
			BaseURL:       baseURL,
			ContextWindow: ctx,
			APIBackend:    types.BackendChat,
			Provider:      p,
			EnvKey:        defaultEnvKey(p),
			ToolMode:      types.ToolModeDirect,
		})
	}
	return out, nil
}

func parseOpenRouterModels(baseURL string, body []byte) ([]Model, error) {
	var list struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			Description         string   `json:"description"`
			ContextLength       int      `json:"context_length"`
			SupportedParameters []string `json:"supported_parameters"`
			Pricing             *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(list.Data))
	for _, d := range list.Data {
		if d.ID == "" {
			continue
		}
		tools := false
		for _, p := range d.SupportedParameters {
			if strings.EqualFold(p, "tools") {
				tools = true
				break
			}
		}
		name := d.Name
		if name == "" {
			name = d.ID
		}
		m := Model{
			ID:            d.ID,
			Model:         d.ID,
			Name:          name,
			Description:   d.Description,
			BaseURL:       baseURL,
			ContextWindow: d.ContextLength,
			APIBackend:    types.BackendChat,
			Provider:      types.ProviderOpenRouter,
			EnvKey:        "OPENROUTER_API_KEY",
			ToolMode:      types.ToolModeDirect,
			SupportsTools: &tools,
		}
		out = append(out, m)
	}
	return out, nil
}

// IsFree reports whether a model id is an OpenRouter free-tier slug.
func IsFree(m Model) bool {
	id := strings.ToLower(m.ID + " " + m.Model + " " + m.Name)
	return strings.Contains(id, ":free") || strings.Contains(id, "(free)")
}
