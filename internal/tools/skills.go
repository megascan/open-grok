package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

// Skill lists project and user SKILL.md files.
type Skill struct{}

func (Skill) Name() string { return "skill" }
func (Skill) Description() string {
	return "Load a skill by name from .entropy/skills, ~/.entropy/skills, or .claude/skills."
}
func (Skill) Access() types.AccessKind { return types.AccessRead }
func (Skill) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"name": map[string]any{"type": "string"},
	}, []string{"name"})
}

func (Skill) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	name := str(unmarshal(args)["name"])
	if name == "" {
		all := DiscoverSkills(tc.CWD)
		if len(all) == 0 {
			return okResult("no skills discovered")
		}
		var b strings.Builder
		for _, s := range all {
			b.WriteString(s.Name)
			b.WriteString(" — ")
			b.WriteString(s.Description)
			b.WriteByte('\n')
		}
		return okResult(b.String())
	}
	for _, s := range DiscoverSkills(tc.CWD) {
		if s.Name == name {
			return okResult(s.Body)
		}
	}
	return errResult("skill not found: " + name)
}

// SkillInfo is a discovered SKILL.md.
type SkillInfo struct {
	Name          string
	Description   string
	Path          string
	Body          string
	UserInvocable bool
}

// DiscoverSkills walks well-known skills directories. Vendor default Cursor/Claude
// skills under their vendor dirs are dropped.
func DiscoverSkills(cwd string) []SkillInfo {
	roots := []string{
		filepath.Join(cwd, ".entropy", "skills"),
		filepath.Join(cwd, ".opengrok", "skills"),
		filepath.Join(cwd, ".agents", "skills"),
		filepath.Join(cwd, ".claude", "skills"),
		filepath.Join(cwd, ".cursor", "skills"),
	}
	if home, ok := config.UserHome(); ok {
		roots = append(roots, filepath.Join(home, "skills"))
	}
	roots = append(roots, filepath.Join(config.Home(), "skills"))
	if cfg, err := config.Load(); err == nil {
		roots = append(roots, cfg.Paths.ExtraSkillDirs...)
	}
	if cfg, err := config.LoadAt(config.ProjectConfigTOML(cwd)); err == nil {
		roots = append(roots, cfg.Paths.ExtraSkillDirs...)
	}
	var out []SkillInfo
	seen := map[string]bool{}
	for _, skillsDir := range roots {
		_ = filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if d.Name() != "SKILL.md" {
				return nil
			}
			info := parseSkill(path)
			if info.Name == "" || seen[info.Name] {
				return nil
			}
			if isVendorDefault(path, info.Name) {
				return nil
			}
			seen[info.Name] = true
			out = append(out, info)
			return nil
		})
	}
	return out
}

func parseSkill(path string) SkillInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillInfo{}
	}
	text := string(data)
	info := SkillInfo{Path: path, Body: text, Name: filepath.Base(filepath.Dir(path))}
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "---"); end >= 0 {
			fm := text[3 : 3+end]
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if k, v, ok := strings.Cut(line, ":"); ok {
					v = strings.TrimSpace(strings.Trim(v, `"'`))
					switch strings.TrimSpace(k) {
					case "name":
						info.Name = v
					case "description":
						info.Description = v
					case "user-invocable":
						info.UserInvocable = v == "true" || v == "yes" || v == "1"
					}
				}
			}
			info.Body = strings.TrimSpace(text[3+end+3:])
		}
	}
	if len(info.Description) > 1024 {
		info.Description = info.Description[:1024]
	}
	return info
}

func isVendorDefault(path, name string) bool {
	p := filepath.ToSlash(path)
	cursor := strings.Contains(p, "/.cursor/")
	claude := strings.Contains(p, "/.claude/")
	switch name {
	case "babysit", "canvas", "create-hook", "create-rule", "create-skill",
		"create-subagent", "loop", "migrate-to-skills", "sdk", "shell",
		"split-to-prs", "statusline", "update-cli-config", "update-cursor-settings":
		return cursor
	case "pdf", "docx", "xlsx", "pptx", "skill-creator":
		return claude
	}
	return false
}
