package agent_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/types"
)

type rt func(*http.Request) (*http.Response, error)

func (f rt) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestTurnLoopWritesFileThroughPlanGate(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	cwd := t.TempDir()
	model := catalog.Model{
		ID:         "mock",
		Model:      "mock",
		Name:       "mock",
		Provider:   types.ProviderFireworks,
		APIBackend: types.BackendChat,
		BaseURL:    "https://example.invalid/v1",
	}
	sess, err := agent.NewSession(cwd, model, true)
	if err != nil {
		t.Fatal(err)
	}

	n := 0
	sseTool := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"1","function":{"name":"write_file","arguments":"{\"path\":\"n.txt\",\"contents\":\"ok\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n"
	sseDone := `data: {"choices":[{"delta":{"content":"wrote it"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n"
	httpDo := rt(func(r *http.Request) (*http.Response, error) {
		n++
		body := sseDone
		if n == 1 {
			body = sseTool
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})

	err = agent.RunTurn(context.Background(), sess, agent.Runtime{
		Model:  model,
		Bearer: "k",
		HTTP:   sampler.HTTPDoer(httpDo),
	}, "write n.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cwd, "n.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(sess.Dir, "session.json")); err != nil {
		t.Fatal("session must persist")
	}
}

func TestPlanModeBlocksNonPlanEditsEvenWithYOLO(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	cwd := t.TempDir()
	model := catalog.Model{
		ID: "mock", Model: "mock", Name: "mock",
		Provider: types.ProviderKimi, APIBackend: types.BackendChat,
		BaseURL: "https://example.invalid/v1",
	}
	sess, err := agent.NewSession(cwd, model, true)
	if err != nil {
		t.Fatal(err)
	}
	sess.Plan.Enter(filepath.Join(sess.Dir, "plan.md"))

	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"1","function":{"name":"write_file","arguments":"{\"path\":\"secret.go\",\"contents\":\"x\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n"
	done := `data: {"choices":[{"delta":{"content":"blocked"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n"
	n := 0
	httpDo := rt(func(r *http.Request) (*http.Response, error) {
		n++
		body := done
		if n == 1 {
			body = sse
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	if err := agent.RunTurn(context.Background(), sess, agent.Runtime{Model: model, Bearer: "k", HTTP: sampler.HTTPDoer(httpDo)}, "edit", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "secret.go")); err == nil {
		t.Fatal("plan mode must refuse the edit")
	}
}
