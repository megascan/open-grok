package catalog_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

func TestFetchOpenRouterParsesToolsAndKeepsProvider(t *testing.T) {
	body := map[string]any{
		"data": []map[string]any{
			{
				"id":                   "acme/widget:free",
				"name":                 "Widget Free",
				"context_length":       8192,
				"supported_parameters": []string{"temperature"},
			},
			{
				"id":                   "acme/pro",
				"name":                 "Pro",
				"context_length":       128000,
				"supported_parameters": []string{"tools", "temperature"},
			},
		},
	}
	raw, _ := json.Marshal(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HTTP-Referer") == "" || r.Header.Get("X-Title") == "" {
			t.Errorf("OpenRouter /models must send attribution headers")
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	c := catalog.LiveClient{HTTP: srv.Client()}
	list, err := c.FetchOpenAIModels(types.ProviderOpenRouter, srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d", len(list))
	}
	for _, m := range list {
		if m.Provider != types.ProviderOpenRouter {
			t.Fatalf("provider must come from the caller, got %s", m.Provider)
		}
	}
	if list[0].ToolsEnabled() {
		t.Fatal("widget:free without tools param must disable tools")
	}
	if !list[1].ToolsEnabled() {
		t.Fatal("pro with tools param must enable tools")
	}
	_ = io.Discard
}
