package marketplace_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/marketplace"
)

// listFixture is a JSON catalog payload. Tests return this from httptest;
// they never clone a git repo or fetch grok.com.
const listFixture = `{
	"sources": [
		{
			"sourceName": "official",
			"sourceKind": "git",
			"sourceUrlOrPath": "https://github.com/xai-org/grok-plugins.git",
			"plugins": [
				{
					"name": "demo",
					"version": "2.0.0",
					"description": "Demo plugin",
					"category": "development",
					"author": "xai",
					"tags": ["cli"],
					"keywords": ["search"],
					"domains": ["example.com"],
					"homepage": "https://example.com/demo",
					"relativePath": "plugins/demo",
					"skillCount": 1,
					"hasHooks": true,
					"hasAgents": false,
					"hasMcp": false,
					"installStatus": "not_installed",
					"installedVersion": null
				},
				{
					"name": "office",
					"version": "1.0.0",
					"description": "Office helpers",
					"category": null,
					"author": null,
					"tags": [],
					"relativePath": "plugins/office",
					"skillCount": 0,
					"hasHooks": false,
					"hasAgents": true,
					"hasMcp": true,
					"installStatus": "installed",
					"installedVersion": "0.9.0"
				}
			],
			"error": null
		},
		{
			"sourceName": "broken",
			"sourceKind": "local",
			"sourceUrlOrPath": "/tmp/missing-market",
			"plugins": [],
			"error": "Directory not found: /tmp/missing-market"
		}
	]
}`

func TestListPostsJSONFixture(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotCT = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		if r.Host == "grok.com" || strings.Contains(r.URL.String(), "grok.com") {
			t.Errorf("must not call grok.com: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listFixture))
	}))
	defer srv.Close()

	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	resp, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method %s, want POST", gotMethod)
	}
	if gotPath != "/x.ai/marketplace/list" {
		t.Fatalf("path %s", gotPath)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Fatalf("content-type %s", gotCT)
	}
	if strings.TrimSpace(string(gotBody)) != "{}" {
		t.Fatalf("list body %s", gotBody)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("sources %d", len(resp.Sources))
	}
	if resp.Sources[0].SourceName != "official" {
		t.Fatalf("source %s", resp.Sources[0].SourceName)
	}
	if n := len(resp.Sources[0].Plugins); n != 2 {
		t.Fatalf("plugins %d", n)
	}
	p := resp.Sources[0].Plugins[0]
	if p.Name != "demo" || p.RelativePath != "plugins/demo" || p.InstallStatus != "not_installed" {
		t.Fatalf("plugin %+v", p)
	}
	if resp.Sources[1].Error == nil || !strings.Contains(*resp.Sources[1].Error, "Directory not found") {
		t.Fatalf("scan error %+v", resp.Sources[1].Error)
	}
}

func TestListAcceptsACPResultWrapper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":` + listFixture + `}`))
	}))
	defer srv.Close()

	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	resp, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sources) != 2 || resp.Sources[0].Plugins[0].Name != "demo" {
		t.Fatalf("%+v", resp)
	}
}

func TestActionInstallPostsSnakeCaseUnion(t *testing.T) {
	var gotPath string
	var gotReq marketplace.ActionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("decode request: %v (%s)", err, body)
		}
		if strings.Contains(string(body), `"pluginRelativePath"`) {
			t.Errorf("action fields must be snake_case: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "success",
			"message": "Installed from official: plugins/demo (key: demo-aaaa)",
			"requiresReload": false,
			"requiresRestart": false
		}`))
	}))
	defer srv.Close()

	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	out, err := c.Action(context.Background(), marketplace.ActionRequest{
		SessionID: "sess-9",
		Action:    marketplace.Install("https://github.com/xai-org/grok-plugins.git", "plugins/demo"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/x.ai/marketplace/action" {
		t.Fatalf("path %s", gotPath)
	}
	if gotReq.SessionID != "sess-9" || gotReq.Action.Type != marketplace.ActionInstall {
		t.Fatalf("request %+v", gotReq)
	}
	if gotReq.Action.SourceURLOrPath != "https://github.com/xai-org/grok-plugins.git" {
		t.Fatalf("source %s", gotReq.Action.SourceURLOrPath)
	}
	if gotReq.Action.PluginRelativePath != "plugins/demo" {
		t.Fatalf("rel %s", gotReq.Action.PluginRelativePath)
	}
	if out.Status != marketplace.StatusSuccess {
		t.Fatalf("status %s", out.Status)
	}
	if !strings.Contains(out.Message, "plugins/demo") {
		t.Fatalf("message %s", out.Message)
	}
}

func TestActionAddSourceOutcomeFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"add_source"`) {
			t.Errorf("body %s", body)
		}
		_, _ = w.Write([]byte(`{
			"status": "success",
			"message": "Added marketplace source: extra (https://example.com/extra.git)",
			"requiresReload": true,
			"requiresRestart": false
		}`))
	}))
	defer srv.Close()

	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	out, err := c.Action(context.Background(), marketplace.ActionRequest{
		SessionID: "s",
		Action:    marketplace.AddSource("https://example.com/extra.git"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.RequiresReload || out.RequiresRestart {
		t.Fatalf("flags %+v", out)
	}
}

func TestListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	_, err := c.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err %v", err)
	}
}

func TestEmptyBaseURL(t *testing.T) {
	c := marketplace.Client{}
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("expected empty BaseURL error")
	}
}

func TestCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listFixture))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := marketplace.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	if _, err := c.List(ctx); err == nil {
		t.Fatal("expected canceled context error")
	}
}
