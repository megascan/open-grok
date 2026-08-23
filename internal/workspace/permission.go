// Package workspace implements the permission pipeline and plan-mode edit gate.
//
// Order: plan-mode edit gate → (hooks fail-open) → plan-file auto-approve →
// permission manager. Plan mode is not permission YOLO.
package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

// Decision is the outcome of the permission pipeline.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
	Ask   Decision = "ask"
)

// Request is one tool invocation presented to the pipeline.
type Request struct {
	Tool       string
	Access     types.AccessKind
	Path       string
	Command    string
	AlwaysYOLO bool
	AutoMode   bool
	PlanActive bool
	PlanFile   string
	AllowRules []string
	DenyRules  []string
	ExtraRoots []string
}

// Check runs the permission pipeline. Deny wins over allow.
func Check(req Request) (Decision, string) {
	if reason, denied := planModeEditGate(req); denied {
		return Deny, reason
	}
	if req.PlanActive && req.Access == types.AccessEdit && isPlanFile(req.Path, req.PlanFile) {
		return Allow, "plan-file auto-approve"
	}
	for _, rule := range req.DenyRules {
		if matchRule(rule, req) {
			return Deny, "deny rule: " + rule
		}
	}
	if req.AlwaysYOLO {
		return Allow, "always-approve"
	}
	for _, rule := range req.AllowRules {
		if matchRule(rule, req) {
			return Allow, "allow rule: " + rule
		}
	}
	if extraRootAllows(req) {
		return Allow, "working-dir allow"
	}
	if req.AutoMode && req.Access != types.AccessBash {
		return Allow, "auto permission mode"
	}
	switch req.Access {
	case types.AccessRead, types.AccessNone:
		return Allow, "read/none auto-allow"
	default:
		return Ask, "needs approval"
	}
}

func extraRootAllows(req Request) bool {
	if req.Path == "" || len(req.ExtraRoots) == 0 {
		return false
	}
	if req.Access != types.AccessRead && req.Access != types.AccessEdit {
		return false
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil {
		return false
	}
	for _, root := range req.ExtraRoots {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == r || strings.HasPrefix(abs, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// planModeEditGate is the hard reject that runs before hooks/permissions.
func planModeEditGate(req Request) (string, bool) {
	if !req.PlanActive || req.Access != types.AccessEdit {
		return "", false
	}
	if isPlanFile(req.Path, req.PlanFile) {
		return "", false
	}
	return fmt.Sprintf("plan mode: refusing edit of %s (only %s is writable)", req.Path, req.PlanFile), true
}

func isPlanFile(path, planFile string) bool {
	if path == "" || planFile == "" {
		return false
	}
	a, _ := filepath.Abs(path)
	b, _ := filepath.Abs(planFile)
	return filepath.Clean(a) == filepath.Clean(b)
}

func matchRule(rule string, req Request) bool {
	rule = strings.TrimSpace(rule)
	if rule == "*" || strings.EqualFold(rule, req.Tool) {
		return true
	}
	if strings.HasPrefix(rule, "bash:") && req.Access == types.AccessBash {
		return strings.Contains(req.Command, strings.TrimPrefix(rule, "bash:"))
	}
	name, pat, ok := splitToolPattern(rule)
	if ok && strings.EqualFold(name, req.Tool) {
		if pat == "" || pat == "*" {
			return true
		}
		hay := req.Command
		if hay == "" {
			hay = req.Path
		}
		return strings.Contains(strings.ToLower(hay), strings.ToLower(strings.TrimSuffix(pat, "*")))
	}
	return false
}

// splitToolPattern parses Claude/Open Grok compact rules like Bash(*) or Read(src/**).
func splitToolPattern(rule string) (name, pat string, ok bool) {
	open := strings.IndexByte(rule, '(')
	if open <= 0 || !strings.HasSuffix(rule, ")") {
		return "", "", false
	}
	return rule[:open], strings.TrimSpace(rule[open+1 : len(rule)-1]), true
}

// PlanTracker is a fresh Inactive tracker per session (including subagents).
type PlanTracker struct {
	Active   bool
	PlanFile string
}

// Enter activates plan mode. Children get a fresh Inactive tracker.
func (p *PlanTracker) Enter(planFile string) {
	p.Active = true
	p.PlanFile = planFile
}

// Exit returns to Inactive.
func (p *PlanTracker) Exit() {
	p.Active = false
}

// NewInactive is the tracker handed to subagents.
func NewInactive() PlanTracker { return PlanTracker{} }
