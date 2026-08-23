package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// WebSearch is a client-side fallback search used when the provider has no
// native hosted web search. It queries DuckDuckGo's instant-answer API.
type WebSearch struct {
	HTTP *http.Client
}

func (WebSearch) Name() string { return "web_search" }
func (WebSearch) Description() string {
	return "Search the public web. Optional allowed_domains restricts results."
}
func (WebSearch) Access() types.AccessKind { return types.AccessWeb }
func (w WebSearch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"query":           map[string]any{"type": "string"},
		"allowed_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, []string{"query"})
}

func (w WebSearch) Execute(ctx context.Context, _ Context, args json.RawMessage) Result {
	m := unmarshal(args)
	query := str(m["query"])
	if query == "" {
		return errResult("query is required")
	}
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errResult(err.Error())
	}
	req.Header.Set("User-Agent", "entropy")
	client := w.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errResult(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return okResult(string(body))
}

// WebFetch retrieves a URL as text.
type WebFetch struct {
	HTTP *http.Client
}

func (WebFetch) Name() string { return "web_fetch" }
func (WebFetch) Description() string {
	return "Fetch a public HTTP(S) URL and return a truncated text body."
}
func (WebFetch) Access() types.AccessKind { return types.AccessWeb }
func (WebFetch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"url": map[string]any{"type": "string"},
	}, []string{"url"})
}

func (w WebFetch) Execute(ctx context.Context, _ Context, args json.RawMessage) Result {
	m := unmarshal(args)
	raw := str(m["url"])
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errResult("url must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errResult(err.Error())
	}
	req.Header.Set("User-Agent", "entropy")
	client := w.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errResult(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200_000))
	text := string(body)
	if !strings.Contains(resp.Header.Get("Content-Type"), "text") &&
		!strings.Contains(resp.Header.Get("Content-Type"), "json") &&
		!strings.Contains(resp.Header.Get("Content-Type"), "xml") {
		return errResult(fmt.Sprintf("non-text content-type %s", resp.Header.Get("Content-Type")))
	}
	return okResult(fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, text))
}
