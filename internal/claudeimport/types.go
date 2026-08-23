// Package claudeimport scans ~/.claude and project .claude/ settings and
// writes selected items into Entropy config under ~/.entropy (never ~/.grok).
//
// Item kinds and destinations match the Rust xai-grok-shell claude_import
// scanner: permissions, env, MCP servers, command hooks, and extra skill/rule
// path entries, split into global vs project scope.
package claudeimport

import "github.com/megascan/open-grok/internal/config"

// Scope is where an imported item is written.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Kind is one ImportableItem discriminant from the Rust scanner.
type Kind string

const (
	KindPermission Kind = "permission"
	KindEnv        Kind = "env"
	KindMCP        Kind = "mcp"
	KindHook       Kind = "hook"
	KindPath       Kind = "path"
)

// PathKind maps to [paths] extra_skill_dirs vs extra_rule_dirs.
type PathKind string

const (
	PathSkill PathKind = "skill"
	PathRule  PathKind = "rule"
)

// Item is one selectable Claude setting.
type Item struct {
	Scope    Scope
	Kind     Kind
	Label    string
	Detail   string
	Action   string // allow / deny / ask
	Rule     string // compact Tool(pattern) string
	Key      string
	Value    string
	MCPName  string
	MCP      config.MCP
	Event    string
	Matcher  string
	Command  string
	Timeout  uint64
	PathKind PathKind
	Path     string
}

// Plan is the Rust ImportPlan (global + project items, flattened with Scope).
type Plan struct {
	Items []Item
}

// Empty reports whether the scan found nothing.
func (p Plan) Empty() bool { return len(p.Items) == 0 }

// Result is apply_import's count + modified files.
type Result struct {
	GlobalCount   int
	ProjectCount  int
	ModifiedFiles []string
}

// Total is global + project writes.
func (r Result) Total() int { return r.GlobalCount + r.ProjectCount }
