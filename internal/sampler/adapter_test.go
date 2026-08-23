package sampler_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/types"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestChatStreamParsesToolCallsAndText(t *testing.T) {
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"ls\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	var gotURL string
	a := sampler.Adapter{
		Provider: types.ProviderFireworks,
		Backend:  types.BackendChat,
		BaseURL:  "https://api.fireworks.ai/inference/v1",
		Bearer:   "secret",
		HTTP: roundTrip(func(r *http.Request) (*http.Response, error) {
			gotURL = r.URL.String()
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Fatalf("missing bearer")
			}
			if r.Header.Get("x-grok-session-id") != "" {
				t.Fatal("fireworks must not send xAI private headers")
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(sse)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	ch, err := a.Stream(context.Background(), types.SampleRequest{
		Model:    "accounts/fireworks/models/glm-5p2",
		Messages: []types.Message{{Role: types.RoleUser, Content: "list"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var tools []types.ToolCall
	for ev := range ch {
		switch ev.Kind {
		case types.StreamDelta:
			text.WriteString(ev.Text)
		case types.StreamToolCall:
			if ev.ToolCall != nil {
				tools = append(tools, *ev.ToolCall)
			}
		}
	}
	if !strings.Contains(gotURL, "/chat/completions") {
		t.Fatalf("url %s", gotURL)
	}
	if text.String() != "hi " {
		t.Fatalf("text %q", text.String())
	}
	if len(tools) != 1 || tools[0].Name != "bash" || tools[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("tools %#v", tools)
	}
}

func TestXAIAdapterSendsPrivateHeaders(t *testing.T) {
	a := sampler.Adapter{
		Provider: types.ProviderXAI,
		Backend:  types.BackendChat,
		BaseURL:  "https://api.x.ai/v1",
		Bearer:   "k",
		HTTP: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("x-grok-session-id") != "sess-1" {
				t.Fatalf("xAI adapter owns x-grok headers, got %q", r.Header.Get("x-grok-session-id"))
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	ch, err := a.Stream(context.Background(), types.SampleRequest{
		Model:     "grok-4.6",
		Messages:  []types.Message{{Role: types.RoleUser, Content: "hi"}},
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
}
