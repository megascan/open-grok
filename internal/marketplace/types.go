// Package marketplace is an HTTP client for the Open Grok marketplace catalog
// used by the extensions overlay.
//
// The Rust pager talks ACP ext methods `x.ai/marketplace/list` and
// `x.ai/marketplace/action` (see xai-hooks-plugins-types). This package maps
// those methods onto HTTP for the Go TUI and tests:
//
//	POST {BaseURL}/x.ai/marketplace/list    → ListResponse
//	POST {BaseURL}/x.ai/marketplace/action  → ActionOutcome
//
// List POSTs an empty JSON object (`{}`). Action POSTs an ActionRequest body.
// Responses may be the DTO itself or an ACP-style `{ "result": <dto> }` wrapper
// (the pager unwraps the same way).
//
// Wire JSON is camelCase except Action, which is a serde internally-tagged
// union (`tag = "type"`) with snake_case variant names and snake_case fields.
//
// Tests must use net/http/httptest. This client never scrapes grok.com HTML
// and never clones git repositories; catalog payloads are JSON fixtures.
package marketplace

import (
	"encoding/json"
	"fmt"
)

// ListResponse is MarketplaceListResponse.
type ListResponse struct {
	Sources []ScanResult `json:"sources"`
}

// ScanResult is MarketplaceScanResult.
type ScanResult struct {
	SourceName      string        `json:"sourceName"`
	SourceKind      string        `json:"sourceKind"`
	SourceURLOrPath string        `json:"sourceUrlOrPath"`
	Plugins         []PluginEntry `json:"plugins"`
	Error           *string       `json:"error"`
}

// PluginEntry is MarketplacePluginEntry.
type PluginEntry struct {
	Name             string            `json:"name"`
	Version          *string           `json:"version"`
	Description      *string           `json:"description"`
	Category         *string           `json:"category"`
	Author           *string           `json:"author"`
	Tags             []string          `json:"tags"`
	Keywords         []string          `json:"keywords"`
	Domains          []string          `json:"domains"`
	Homepage         *string           `json:"homepage"`
	RelativePath     string            `json:"relativePath"`
	SkillCount       int               `json:"skillCount"`
	HasHooks         bool              `json:"hasHooks"`
	HasAgents        bool              `json:"hasAgents"`
	HasMcp           bool              `json:"hasMcp"`
	InstallStatus    string            `json:"installStatus"`
	InstalledVersion *string           `json:"installedVersion"`
	Components       *PluginComponents `json:"components,omitempty"`
	RemoteURL        *string           `json:"remoteUrl,omitempty"`
	RemoteRef        *string           `json:"remoteRef,omitempty"`
	RemoteSHA        *string           `json:"remoteSha,omitempty"`
	RemoteSubdir     *string           `json:"remoteSubdir,omitempty"`
}

// PluginComponents is the catalog inventory on a plugin entry.
type PluginComponents struct {
	Skills     []ComponentItem `json:"skills,omitempty"`
	Commands   []ComponentItem `json:"commands,omitempty"`
	Agents     []ComponentItem `json:"agents,omitempty"`
	MCPServers []ComponentItem `json:"mcpServers,omitempty"`
	Hooks      []ComponentItem `json:"hooks,omitempty"`
	LSPServers []ComponentItem `json:"lspServers,omitempty"`
}

// ComponentItem is one catalog-listed skill, command, agent, or similar.
type ComponentItem struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// ActionRequest is MarketplaceActionRequest.
type ActionRequest struct {
	SessionID string `json:"sessionId"`
	Action    Action `json:"action"`
}

// Action variant tags (serde rename_all = "snake_case").
const (
	ActionRefresh      = "refresh"
	ActionInstall      = "install"
	ActionUpdate       = "update"
	ActionUninstall    = "uninstall"
	ActionAddSource    = "add_source"
	ActionRemoveSource = "remove_source"
)

// Action is MarketplaceAction: internally tagged with snake_case `type`.
//
// Wire field names inside the union are snake_case (not camelCase):
// source_url_or_path, plugin_relative_path, url.
type Action struct {
	Type string

	// SourceURLOrPath is the canonical git URL or local path.
	// Refresh: optional filter (empty → JSON null).
	// Install / Update / Uninstall / RemoveSource: required.
	SourceURLOrPath string

	// PluginRelativePath is required for install, update, and uninstall.
	PluginRelativePath string

	// URL is the git URL for add_source.
	URL string
}

// Refresh builds a refresh action. filter is the optional source URL/path.
func Refresh(filter *string) Action {
	a := Action{Type: ActionRefresh}
	if filter != nil {
		a.SourceURLOrPath = *filter
	}
	return a
}

// Install builds an install action.
func Install(sourceURLOrPath, pluginRelativePath string) Action {
	return Action{Type: ActionInstall, SourceURLOrPath: sourceURLOrPath, PluginRelativePath: pluginRelativePath}
}

// Update builds an update action.
func Update(sourceURLOrPath, pluginRelativePath string) Action {
	return Action{Type: ActionUpdate, SourceURLOrPath: sourceURLOrPath, PluginRelativePath: pluginRelativePath}
}

// Uninstall builds an uninstall action.
func Uninstall(sourceURLOrPath, pluginRelativePath string) Action {
	return Action{Type: ActionUninstall, SourceURLOrPath: sourceURLOrPath, PluginRelativePath: pluginRelativePath}
}

// AddSource builds an add_source action.
func AddSource(url string) Action {
	return Action{Type: ActionAddSource, URL: url}
}

// RemoveSource builds a remove_source action.
func RemoveSource(sourceURLOrPath string) Action {
	return Action{Type: ActionRemoveSource, SourceURLOrPath: sourceURLOrPath}
}

// MarshalJSON emits the serde internally-tagged Action union.
func (a Action) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case ActionRefresh:
		var filter any
		if a.SourceURLOrPath != "" {
			filter = a.SourceURLOrPath
		}
		return json.Marshal(struct {
			Type            string `json:"type"`
			SourceURLOrPath any    `json:"source_url_or_path"`
		}{Type: a.Type, SourceURLOrPath: filter})
	case ActionInstall, ActionUpdate, ActionUninstall:
		return json.Marshal(struct {
			Type               string `json:"type"`
			SourceURLOrPath    string `json:"source_url_or_path"`
			PluginRelativePath string `json:"plugin_relative_path"`
		}{Type: a.Type, SourceURLOrPath: a.SourceURLOrPath, PluginRelativePath: a.PluginRelativePath})
	case ActionAddSource:
		return json.Marshal(struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}{Type: a.Type, URL: a.URL})
	case ActionRemoveSource:
		return json.Marshal(struct {
			Type            string `json:"type"`
			SourceURLOrPath string `json:"source_url_or_path"`
		}{Type: a.Type, SourceURLOrPath: a.SourceURLOrPath})
	default:
		return nil, fmt.Errorf("marketplace: unknown action type %q", a.Type)
	}
}

// UnmarshalJSON reads the serde internally-tagged Action union.
func (a *Action) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Type {
	case ActionRefresh:
		var raw struct {
			SourceURLOrPath *string `json:"source_url_or_path"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		a.Type = probe.Type
		if raw.SourceURLOrPath != nil {
			a.SourceURLOrPath = *raw.SourceURLOrPath
		}
		return nil
	case ActionInstall, ActionUpdate, ActionUninstall:
		var raw struct {
			SourceURLOrPath    string `json:"source_url_or_path"`
			PluginRelativePath string `json:"plugin_relative_path"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		a.Type = probe.Type
		a.SourceURLOrPath = raw.SourceURLOrPath
		a.PluginRelativePath = raw.PluginRelativePath
		return nil
	case ActionAddSource:
		var raw struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		a.Type = probe.Type
		a.URL = raw.URL
		return nil
	case ActionRemoveSource:
		var raw struct {
			SourceURLOrPath string `json:"source_url_or_path"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		a.Type = probe.Type
		a.SourceURLOrPath = raw.SourceURLOrPath
		return nil
	default:
		return fmt.Errorf("marketplace: unknown action type %q", probe.Type)
	}
}

// OutcomeStatus is the snake_case ActionOutcome.status enum.
type OutcomeStatus string

const (
	StatusSuccess              OutcomeStatus = "success"
	StatusValidationError      OutcomeStatus = "validation_error"
	StatusConfirmationRequired OutcomeStatus = "confirmation_required"
	StatusNotFound             OutcomeStatus = "not_found"
	StatusInternalError        OutcomeStatus = "internal_error"
	StatusUnsupported          OutcomeStatus = "unsupported"
)

// ActionOutcome is the shared ActionOutcome DTO.
type ActionOutcome struct {
	Status          OutcomeStatus `json:"status"`
	Message         string        `json:"message"`
	RequiresReload  bool          `json:"requiresReload"`
	RequiresRestart bool          `json:"requiresRestart"`
}
