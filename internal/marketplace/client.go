package marketplace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	listPath   = "/x.ai/marketplace/list"
	actionPath = "/x.ai/marketplace/action"
	maxBody    = 8 << 20
)

// Client talks to the marketplace HTTP stand-in for ACP ext methods.
//
// HTTP is optional; nil uses http.DefaultClient. BaseURL is the origin
// (scheme + host, no trailing path) of the marketplace service — typically
// an httptest.Server URL in tests, never grok.com.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

// List POSTs `{}` to `{BaseURL}/x.ai/marketplace/list` and decodes a
// ListResponse. This is the HTTP mapping of ACP `x.ai/marketplace/list`.
func (c *Client) List(ctx context.Context) (ListResponse, error) {
	var out ListResponse
	if err := c.do(ctx, http.MethodPost, listPath, map[string]any{}, &out); err != nil {
		return ListResponse{}, err
	}
	if out.Sources == nil {
		out.Sources = []ScanResult{}
	}
	return out, nil
}

// Action POSTs an ActionRequest to `{BaseURL}/x.ai/marketplace/action` and
// decodes an ActionOutcome. This is the HTTP mapping of ACP
// `x.ai/marketplace/action`. The server is expected to return a JSON fixture;
// the client does not clone git repositories.
func (c *Client) Action(ctx context.Context, req ActionRequest) (ActionOutcome, error) {
	var out ActionOutcome
	if err := c.do(ctx, http.MethodPost, actionPath, req, &out); err != nil {
		return ActionOutcome{}, err
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method, path string, reqBody any, out any) error {
	if c == nil {
		return fmt.Errorf("marketplace: nil client")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("marketplace: empty BaseURL")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marketplace: encode: %w", err)
	}
	u := strings.TrimRight(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("marketplace %s: read: %w", path, err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("marketplace %s: response too large", path)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("marketplace %s: HTTP %d: %s", path, resp.StatusCode, clip(body, 400))
	}
	payload := unwrapResult(body)
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("marketplace %s: decode: %w", path, err)
	}
	return nil
}

// unwrapResult accepts either the DTO or an ACP `{ "result": <dto> }` wrapper,
// matching FetchMarketplaceList in the Rust pager.
func unwrapResult(body []byte) []byte {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return body
	}
	result, ok := wrapper["result"]
	if !ok || len(result) == 0 {
		return body
	}
	_, hasSources := wrapper["sources"]
	_, hasStatus := wrapper["status"]
	if hasSources || hasStatus {
		return body
	}
	return result
}

func clip(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
