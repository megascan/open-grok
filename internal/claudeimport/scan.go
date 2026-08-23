package claudeimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/workspace"
)

func claudeHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func claudeJSON() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// HasPendingImport is true when ~/.claude exists and the user has not imported
// or dismissed (Rust welcome has_claude_import).
func HasPendingImport() bool {
	if config.ClaudeImported() {
		return false
	}
	if _, err := os.Stat(filepath.Join(config.Home(), "claude-import-dismissed")); err == nil {
		return false
	}
	st, err := os.Stat(claudeHome())
	return err == nil && st.IsDir()
}

// Scan walks Claude settings sources for cwd and returns a selectable plan.
func Scan(cwd string) Plan {
	var p Plan
	home, _ := os.UserHomeDir()
	globalClaude := filepath.Join(home, ".claude")

	for _, path := range settingsPaths(cwd) {
		scope := ScopeProject
		if home != "" && strings.HasPrefix(path, globalClaude) {
			scope = ScopeGlobal
		}
		scanSettingsFile(path, scope, &p)
	}
	scanClaudeJSONMCP(&p)
	scanMCPJSON(cwd, &p)
	scanPathDirs(cwd, &p)
	return p
}

func settingsPaths(cwd string) []string {
	var out []string
	home := claudeHome()
	for _, name := range []string{"settings.json", "settings.local.json"} {
		out = append(out, filepath.Join(home, name))
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	repo, hasRepo := workspace.GitRoot(dir)
	userHome, _ := os.UserHomeDir()
	userHome = filepath.Clean(userHome)
	for {
		for _, name := range []string{"settings.json", "settings.local.json"} {
			out = append(out, filepath.Join(dir, ".claude", name))
		}
		if hasRepo && filepath.Clean(dir) == filepath.Clean(repo) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if userHome != "" && filepath.Clean(parent) == userHome {
			break
		}
		dir = parent
	}
	return out
}

func scanSettingsFile(path string, scope Scope, p *Plan) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if perms, ok := root["permissions"].(map[string]any); ok {
		for _, action := range []string{"allow", "deny", "ask"} {
			arr, _ := perms[action].([]any)
			for _, v := range arr {
				s, _ := v.(string)
				if strings.TrimSpace(s) == "" {
					continue
				}
				p.Items = append(p.Items, Item{
					Scope:  scope,
					Kind:   KindPermission,
					Action: action,
					Rule:   s,
					Label:  action + " " + s,
					Detail: string(scope),
				})
			}
		}
	}
	if env, ok := root["env"].(map[string]any); ok {
		for k, v := range env {
			val, _ := v.(string)
			p.Items = append(p.Items, Item{
				Scope:  scope,
				Kind:   KindEnv,
				Key:    k,
				Value:  val,
				Label:  k + " = <redacted, " + strconv.Itoa(len(val)) + " chars>",
				Detail: string(scope) + " env",
			})
		}
	}
	if servers, ok := root["mcpServers"].(map[string]any); ok {
		appendMCPMap(servers, scope, p)
	}
	extractHooks(root, scope, p)
}

func extractHooks(root map[string]any, scope Scope, p *Plan) {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return
	}
	for event, groupsVal := range hooks {
		groups, ok := groupsVal.([]any)
		if !ok {
			continue
		}
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			matcher, _ := gm["matcher"].(string)
			handlers, _ := gm["hooks"].([]any)
			for _, h := range handlers {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				typ, _ := hm["type"].(string)
				if typ != "" && typ != "command" {
					continue
				}
				cmd, _ := hm["command"].(string)
				if cmd == "" {
					continue
				}
				var timeout uint64
				switch t := hm["timeout"].(type) {
				case float64:
					timeout = uint64(t)
				}
				m := matcher
				if m == "" {
					m = "*"
				}
				p.Items = append(p.Items, Item{
					Scope:   scope,
					Kind:    KindHook,
					Event:   event,
					Matcher: matcher,
					Command: cmd,
					Timeout: timeout,
					Label:   event + " [" + m + "] " + cmd,
					Detail:  string(scope) + " hook",
				})
			}
		}
	}
}

func scanClaudeJSONMCP(p *Plan) {
	raw, err := os.ReadFile(claudeJSON())
	if err != nil {
		return
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if servers, ok := root["mcpServers"].(map[string]any); ok {
		appendMCPMap(servers, ScopeGlobal, p)
	}
}

func scanMCPJSON(cwd string, p *Plan) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	seen := map[string]bool{}
	home, _ := os.UserHomeDir()
	home = filepath.Clean(home)
	repo, hasRepo := workspace.GitRoot(dir)
	for {
		path := filepath.Join(dir, ".mcp.json")
		if !seen[path] {
			seen[path] = true
			raw, err := os.ReadFile(path)
			if err == nil {
				var root map[string]any
				if json.Unmarshal(raw, &root) == nil {
					if servers, ok := root["mcpServers"].(map[string]any); ok {
						appendMCPMap(servers, ScopeProject, p)
					}
				}
			}
		}
		if hasRepo && filepath.Clean(dir) == filepath.Clean(repo) {
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		if home != "" && filepath.Clean(parent) == home {
			return
		}
		dir = parent
	}
}

func appendMCPMap(servers map[string]any, scope Scope, p *Plan) {
	for name, raw := range servers {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		mcp := parseMCP(m)
		p.Items = append(p.Items, Item{
			Scope:   scope,
			Kind:    KindMCP,
			MCPName: name,
			MCP:     mcp,
			Label:   name,
			Detail:  string(scope) + " MCP",
		})
	}
}

func parseMCP(m map[string]any) config.MCP {
	out := config.MCP{}
	out.Command, _ = m["command"].(string)
	out.URL, _ = m["url"].(string)
	if args, ok := m["args"].([]any); ok {
		for _, a := range args {
			if s, ok := a.(string); ok {
				out.Args = append(out.Args, s)
			}
		}
	}
	if env, ok := m["env"].(map[string]any); ok {
		out.Env = map[string]string{}
		for k, v := range env {
			if s, ok := v.(string); ok {
				out.Env[k] = s
			}
		}
	}
	return out
}

func scanPathDirs(cwd string, p *Plan) {
	home, _ := os.UserHomeDir()
	added := map[string]bool{}
	if home != "" {
		for _, kind := range []PathKind{PathSkill, PathRule} {
			dir := filepath.Join(home, ".claude", string(kind)+"s")
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				added[filepath.Clean(dir)] = true
				p.Items = append(p.Items, pathItem(ScopeGlobal, kind, dir))
			}
		}
	}
	root, ok := workspace.GitRoot(cwd)
	if !ok {
		root = cwd
	}
	for _, kind := range []PathKind{PathSkill, PathRule} {
		dir := filepath.Join(root, ".claude", string(kind)+"s")
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			if added[filepath.Clean(dir)] {
				continue
			}
			p.Items = append(p.Items, pathItem(ScopeProject, kind, dir))
		}
	}
}

func pathItem(scope Scope, kind PathKind, dir string) Item {
	label := "extra " + string(kind) + " dir"
	return Item{
		Scope:    scope,
		Kind:     KindPath,
		PathKind: kind,
		Path:     dir,
		Label:    label + "  " + dir,
		Detail:   string(scope),
	}
}
