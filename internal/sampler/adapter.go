// Package sampler implements credential-free HTTP adapters for each API backend.
//
// Adapters never resolve or refresh credentials. The caller supplies a bearer
// and a Provider taken from model metadata.
package sampler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// HTTPDoer is the test seam for outbound inference.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Adapter streams a SampleRequest against one provider + backend.
type Adapter struct {
	Provider types.Provider
	Backend  types.ApiBackend
	BaseURL  string
	Bearer   string
	HTTP     HTTPDoer
}

func (a Adapter) client() HTTPDoer {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// Stream yields incremental events until done or ctx cancel.
func (a Adapter) Stream(ctx context.Context, req types.SampleRequest) (<-chan types.StreamEvent, error) {
	switch a.Backend {
	case types.BackendMessages:
		return a.streamMessages(ctx, req)
	case types.BackendResponses:
		return a.streamResponses(ctx, req)
	default:
		return a.streamChat(ctx, req)
	}
}

func (a Adapter) streamChat(ctx context.Context, req types.SampleRequest) (<-chan types.StreamEvent, error) {
	payload := chatBody(req)
	return a.streamSSE(ctx, strings.TrimRight(a.BaseURL, "/")+"/chat/completions", payload, req.SessionID, parseChatSSE)
}

func (a Adapter) streamResponses(ctx context.Context, req types.SampleRequest) (<-chan types.StreamEvent, error) {
	payload := responsesBody(req)
	return a.streamSSE(ctx, strings.TrimRight(a.BaseURL, "/")+"/responses", payload, req.SessionID, parseResponsesSSE)
}

func (a Adapter) streamMessages(ctx context.Context, req types.SampleRequest) (<-chan types.StreamEvent, error) {
	payload := messagesBody(req)
	return a.streamSSE(ctx, strings.TrimRight(a.BaseURL, "/")+"/messages", payload, req.SessionID, parseMessagesSSE)
}

func (a Adapter) streamSSE(ctx context.Context, url string, payload any, sessionID string, parse func([]byte, *sseState) []types.StreamEvent) (<-chan types.StreamEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if a.Bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.Bearer)
	}
	if a.Provider == types.ProviderOpenRouter {
		httpReq.Header.Set("HTTP-Referer", "https://github.com/megascan/open-grok")
		httpReq.Header.Set("X-Title", "Open Grok")
	}
	if a.Provider.Profile().RequestMetadata == types.MetadataXGrok {
		// Private xAI headers stay on the xAI adapter only.
		httpReq.Header.Set("x-grok-session-id", sessionID)
	}

	resp, err := a.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d: %s", a.Provider, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	out := make(chan types.StreamEvent, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		st := &sseState{}
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				out <- types.StreamEvent{Kind: types.StreamDone}
				return
			}
			for _, ev := range parse([]byte(data), st) {
				select {
				case <-ctx.Done():
					out <- types.StreamEvent{Kind: types.StreamError, Err: ctx.Err()}
					return
				case out <- ev:
				}
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			out <- types.StreamEvent{Kind: types.StreamError, Err: err}
			return
		}
		out <- types.StreamEvent{Kind: types.StreamDone}
	}()
	return out, nil
}

type sseState struct {
	toolArgs map[int]stringBuilder
	toolIDs  map[int]string
	toolName map[int]string
}

func chatBody(req types.SampleRequest) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		item := map[string]any{"role": string(m.Role), "content": m.Content}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				})
			}
			item["tool_calls"] = calls
		}
		msgs = append(msgs, item)
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		body["tools"] = tools
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	return body
}

func responsesBody(req types.SampleRequest) map[string]any {
	input := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "system" {
			role = "developer"
		}
		item := map[string]any{"role": role, "content": m.Content}
		input = append(input, item)
	}
	body := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": true,
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			})
		}
		body["tools"] = tools
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	return body
}

func messagesBody(req types.SampleRequest) map[string]any {
	var system string
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == types.RoleSystem {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		msgs = append(msgs, map[string]any{
			"role":    string(m.Role),
			"content": m.Content,
		})
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"stream":     true,
		"max_tokens": 8192,
	}
	if system != "" {
		body["system"] = system
	}
	return body
}
