package marketplace_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/marketplace"
)

func TestPluginEntryCamelCaseRoundtrip(t *testing.T) {
	raw := `{
		"name": "demo",
		"version": "1.2.3",
		"description": "A demo plugin",
		"category": "development",
		"author": "xai",
		"tags": ["cli"],
		"keywords": ["search", "index"],
		"domains": ["example.com"],
		"homepage": "https://example.com/demo",
		"relativePath": "plugins/demo",
		"skillCount": 1,
		"hasHooks": true,
		"hasAgents": false,
		"hasMcp": false,
		"installStatus": "not_installed",
		"installedVersion": null,
		"components": {
			"skills": [{"name": "code-review", "description": "Review staged changes"}]
		},
		"remoteUrl": "https://github.com/acme/demo.git",
		"remoteRef": "main",
		"remoteSha": "abc123",
		"remoteSubdir": "plugins/acme"
	}`
	var entry marketplace.PluginEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Name != "demo" || entry.RelativePath != "plugins/demo" {
		t.Fatalf("identity: %+v", entry)
	}
	if entry.Version == nil || *entry.Version != "1.2.3" {
		t.Fatalf("version %+v", entry.Version)
	}
	if entry.Homepage == nil || *entry.Homepage != "https://example.com/demo" {
		t.Fatalf("homepage %+v", entry.Homepage)
	}
	if got := strings.Join(entry.Keywords, ","); got != "search,index" {
		t.Fatalf("keywords %v", entry.Keywords)
	}
	if entry.SkillCount != 1 || !entry.HasHooks || entry.HasAgents || entry.HasMcp {
		t.Fatalf("flags %+v", entry)
	}
	if entry.Components == nil || len(entry.Components.Skills) != 1 {
		t.Fatalf("components %+v", entry.Components)
	}
	if entry.RemoteSubdir == nil || *entry.RemoteSubdir != "plugins/acme" {
		t.Fatalf("remoteSubdir %+v", entry.RemoteSubdir)
	}

	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"relativePath"`, `"skillCount"`, `"hasHooks"`, `"hasAgents"`, `"hasMcp"`,
		`"installStatus"`, `"installedVersion"`, `"remoteUrl"`, `"remoteRef"`,
		`"remoteSha"`, `"remoteSubdir"`, `"mcpServers"`,
	} {
		if want == `"mcpServers"` {
			continue // omitted when empty
		}
		if !strings.Contains(s, want) {
			t.Errorf("missing camelCase %s in %s", want, s)
		}
	}
	for _, forbid := range []string{`"relative_path"`, `"skill_count"`, `"has_hooks"`, `"install_status"`} {
		if strings.Contains(s, forbid) {
			t.Errorf("snake_case leaked: %s in %s", forbid, s)
		}
	}
}

func TestPluginEntryDefaultsWhenOptionalFieldsAbsent(t *testing.T) {
	raw := `{
		"name": "old",
		"version": null,
		"description": null,
		"category": null,
		"author": null,
		"tags": ["legacy"],
		"relativePath": "plugins/old",
		"skillCount": 0,
		"hasHooks": false,
		"hasAgents": false,
		"hasMcp": false,
		"installStatus": "not_installed",
		"installedVersion": null
	}`
	var entry marketplace.PluginEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Homepage != nil {
		t.Fatalf("homepage %v", entry.Homepage)
	}
	if len(entry.Keywords) != 0 || len(entry.Domains) != 0 {
		t.Fatalf("keywords/domains should default empty: %v %v", entry.Keywords, entry.Domains)
	}
	if entry.Components != nil {
		t.Fatalf("components %v", entry.Components)
	}
	if got := entry.Tags; len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("tags %v", got)
	}
}

func TestListResponseCamelCase(t *testing.T) {
	raw := `{
		"sources": [{
			"sourceName": "official",
			"sourceKind": "git",
			"sourceUrlOrPath": "https://github.com/xai-org/grok-plugins.git",
			"plugins": [],
			"error": null
		}]
	}`
	var resp marketplace.ListResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("sources %d", len(resp.Sources))
	}
	src := resp.Sources[0]
	if src.SourceName != "official" || src.SourceKind != "git" {
		t.Fatalf("%+v", src)
	}
	if src.SourceURLOrPath != "https://github.com/xai-org/grok-plugins.git" {
		t.Fatalf("url %s", src.SourceURLOrPath)
	}
	if src.Error != nil {
		t.Fatalf("error %v", src.Error)
	}
	out, _ := json.Marshal(resp)
	s := string(out)
	for _, want := range []string{`"sourceName"`, `"sourceKind"`, `"sourceUrlOrPath"`, `"sources"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, `"source_name"`) || strings.Contains(s, `"source_url_or_path"`) {
		t.Errorf("scan result leaked snake_case: %s", s)
	}
}

func TestActionSnakeCaseTaggedUnion(t *testing.T) {
	cases := []struct {
		name   string
		action marketplace.Action
		want   string
	}{
		{
			name:   "refresh_all",
			action: marketplace.Refresh(nil),
			want:   `{"type":"refresh","source_url_or_path":null}`,
		},
		{
			name:   "refresh_one",
			action: marketplace.Refresh(strPtr("https://example.com/m.git")),
			want:   `{"type":"refresh","source_url_or_path":"https://example.com/m.git"}`,
		},
		{
			name:   "install",
			action: marketplace.Install("https://example.com/m.git", "plugins/demo"),
			want:   `{"type":"install","source_url_or_path":"https://example.com/m.git","plugin_relative_path":"plugins/demo"}`,
		},
		{
			name:   "update",
			action: marketplace.Update("https://example.com/m.git", "plugins/demo"),
			want:   `{"type":"update","source_url_or_path":"https://example.com/m.git","plugin_relative_path":"plugins/demo"}`,
		},
		{
			name:   "uninstall",
			action: marketplace.Uninstall("https://example.com/m.git", "plugins/demo"),
			want:   `{"type":"uninstall","source_url_or_path":"https://example.com/m.git","plugin_relative_path":"plugins/demo"}`,
		},
		{
			name:   "add_source",
			action: marketplace.AddSource("https://example.com/extra.git"),
			want:   `{"type":"add_source","url":"https://example.com/extra.git"}`,
		},
		{
			name:   "remove_source",
			action: marketplace.RemoveSource("https://example.com/extra.git"),
			want:   `{"type":"remove_source","source_url_or_path":"https://example.com/extra.git"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("marshal\n got %s\nwant %s", got, tc.want)
			}
			var back marketplace.Action
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back.Type != tc.action.Type {
				t.Fatalf("type %q vs %q", back.Type, tc.action.Type)
			}
			if back.SourceURLOrPath != tc.action.SourceURLOrPath || back.PluginRelativePath != tc.action.PluginRelativePath || back.URL != tc.action.URL {
				t.Fatalf("roundtrip %+v vs %+v", back, tc.action)
			}
		})
	}
}

func TestActionRequestCamelCaseSessionID(t *testing.T) {
	req := marketplace.ActionRequest{
		SessionID: "sess-1",
		Action:    marketplace.Install("https://example.com/m.git", "plugins/demo"),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"sessionId":"sess-1"`) {
		t.Fatalf("sessionId camelCase missing: %s", s)
	}
	if strings.Contains(s, `"session_id"`) {
		t.Fatalf("session_id leaked: %s", s)
	}
	if !strings.Contains(s, `"type":"install"`) {
		t.Fatalf("action tag: %s", s)
	}
	if !strings.Contains(s, `"plugin_relative_path"`) {
		t.Fatalf("action fields must stay snake_case: %s", s)
	}
}

func TestActionOutcomeCamelCase(t *testing.T) {
	raw := `{
		"status": "success",
		"message": "Installed 1 plugin(s)",
		"requiresReload": true,
		"requiresRestart": false
	}`
	var out marketplace.ActionOutcome
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != marketplace.StatusSuccess || !out.RequiresReload || out.RequiresRestart {
		t.Fatalf("%+v", out)
	}
	got, _ := json.Marshal(out)
	s := string(got)
	if !strings.Contains(s, `"requiresReload"`) || !strings.Contains(s, `"requiresRestart"`) {
		t.Fatalf("camelCase flags: %s", s)
	}
	if strings.Contains(s, `"requires_reload"`) {
		t.Fatalf("snake_case leaked: %s", s)
	}
}

func strPtr(s string) *string { return &s }
