package claudeimport

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/megascan/open-grok/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

// Apply writes selected items into ~/.entropy/config.toml (and project
// .entropy/config.toml), plus hooks/imported-from-claude.json. Marks
// [claude_compat] imported = true on the user config even when the selection
// is empty, matching the Rust slash-command opt-in.
func Apply(cwd string, items []Item) (Result, error) {
	var r Result
	var global, project []Item
	for _, it := range items {
		if it.Scope == ScopeProject {
			project = append(project, it)
		} else {
			global = append(global, it)
		}
	}
	if len(global) > 0 {
		n, files, err := applyScope(config.ConfigTOML(), filepath.Join(config.Home(), "hooks"), global)
		if err != nil {
			return r, err
		}
		r.GlobalCount = n
		r.ModifiedFiles = append(r.ModifiedFiles, files...)
	}
	if len(project) > 0 {
		cfgPath := config.ProjectConfigTOML(cwd)
		hooksDir := filepath.Join(filepath.Dir(cfgPath), "hooks")
		n, files, err := applyScope(cfgPath, hooksDir, project)
		if err != nil {
			return r, err
		}
		r.ProjectCount = n
		r.ModifiedFiles = append(r.ModifiedFiles, files...)
	}
	if err := markImported(); err != nil {
		return r, err
	}
	return r, nil
}

func applyScope(configPath, hooksDir string, items []Item) (int, []string, error) {
	count := 0
	var files []string
	cfg, err := loadFile(configPath)
	if err != nil {
		return 0, nil, err
	}
	if cfg.MCP == nil {
		cfg.MCP = map[string]config.MCP{}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	for _, it := range items {
		switch it.Kind {
		case KindPermission:
			switch it.Action {
			case "deny":
				if !contains(cfg.Permission.Deny, it.Rule) {
					cfg.Permission.Deny = append(cfg.Permission.Deny, it.Rule)
					count++
				}
			case "ask":
				if !contains(cfg.Permission.Ask, it.Rule) {
					cfg.Permission.Ask = append(cfg.Permission.Ask, it.Rule)
					count++
				}
			default:
				if !contains(cfg.Permission.Allow, it.Rule) {
					cfg.Permission.Allow = append(cfg.Permission.Allow, it.Rule)
					count++
				}
			}
		case KindEnv:
			if it.Key != "" {
				cfg.Env[it.Key] = it.Value
				count++
			}
		case KindMCP:
			if it.MCPName != "" {
				cfg.MCP[it.MCPName] = it.MCP
				count++
			}
		case KindPath:
			if it.PathKind == PathSkill && !contains(cfg.Paths.ExtraSkillDirs, it.Path) {
				cfg.Paths.ExtraSkillDirs = append(cfg.Paths.ExtraSkillDirs, it.Path)
				count++
			}
			if it.PathKind == PathRule && !contains(cfg.Paths.ExtraRuleDirs, it.Path) {
				cfg.Paths.ExtraRuleDirs = append(cfg.Paths.ExtraRuleDirs, it.Path)
				count++
			}
		}
	}
	if count > 0 {
		if err := saveFile(configPath, cfg); err != nil {
			return 0, nil, err
		}
		files = append(files, configPath)
	}
	n, hookFile, err := applyHooks(hooksDir, items)
	if err != nil {
		return count, files, err
	}
	count += n
	if hookFile != "" {
		files = append(files, hookFile)
	}
	return count, files, nil
}

func applyHooks(hooksDir string, items []Item) (int, string, error) {
	type handler struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout uint64 `json:"timeout,omitempty"`
	}
	type group struct {
		Matcher string    `json:"matcher,omitempty"`
		Hooks   []handler `json:"hooks"`
	}
	var incoming []Item
	for _, it := range items {
		if it.Kind == KindHook && it.Command != "" {
			incoming = append(incoming, it)
		}
	}
	if len(incoming) == 0 {
		return 0, "", nil
	}
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return 0, "", err
	}
	target := filepath.Join(hooksDir, "imported-from-claude.json")
	root := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		_ = json.Unmarshal(raw, &root)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	added := 0
	for _, it := range incoming {
		arr, _ := hooks[it.Event].([]any)
		dup := false
		for _, g := range arr {
			gm, _ := g.(map[string]any)
			if gm == nil {
				continue
			}
			m, _ := gm["matcher"].(string)
			if m != it.Matcher {
				continue
			}
			hs, _ := gm["hooks"].([]any)
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if hm != nil {
					if cmd, _ := hm["command"].(string); cmd == it.Command {
						dup = true
					}
				}
			}
		}
		if dup {
			continue
		}
		g := group{Matcher: it.Matcher, Hooks: []handler{{Type: "command", Command: it.Command, Timeout: it.Timeout}}}
		encoded, _ := json.Marshal(g)
		var asMap map[string]any
		_ = json.Unmarshal(encoded, &asMap)
		arr = append(arr, asMap)
		hooks[it.Event] = arr
		added++
	}
	root["hooks"] = hooks
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o600); err != nil {
		return 0, "", err
	}
	return added, target, nil
}

func markImported() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.ClaudeCompat.Imported = true
	return config.Save(cfg)
}

func loadFile(path string) (config.File, error) {
	var f config.File
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, err
	}
	if err := toml.Unmarshal(data, &f); err != nil {
		return f, err
	}
	return f, nil
}

func saveFile(path string, f config.File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
