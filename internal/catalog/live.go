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
			ToolMode:      types.ToolModeDirect,
		})
	}
	return out, nil
}
