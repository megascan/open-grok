package workspace_test

import (
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/workspace"
)

func TestPlanModeEditGateBeforeYOLO(t *testing.T) {
	plan := filepath.Join(t.TempDir(), "plan.md")
	req := workspace.Request{
		Tool:       "write_file",
		Access:     types.AccessEdit,
		Path:       filepath.Join(t.TempDir(), "main.go"),
		AlwaysYOLO: true,
		PlanActive: true,
		PlanFile:   plan,
	}
	d, reason := workspace.Check(req)
	if d != workspace.Deny {
		t.Fatalf("plan gate must hard-reject, got %s (%s)", d, reason)
	}
}

func TestPlanFileAutoApprove(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.md")
	req := workspace.Request{
		Tool:       "write_file",
		Access:     types.AccessEdit,
		Path:       plan,
		PlanActive: true,
		PlanFile:   plan,
	}
	d, _ := workspace.Check(req)
	if d != workspace.Allow {
		t.Fatalf("plan.md must auto-approve, got %s", d)
	}
}

func TestDenyRuleBeatsAllow(t *testing.T) {
	req := workspace.Request{
		Tool:       "bash",
		Access:     types.AccessBash,
		Command:    "rm -rf /",
		AlwaysYOLO: true,
		DenyRules:  []string{"bash"},
	}
	d, _ := workspace.Check(req)
	if d != workspace.Deny {
		t.Fatalf("deny must win, got %s", d)
	}
}

func TestClaudeCompactAllowRule(t *testing.T) {
	d, _ := workspace.Check(workspace.Request{
		Tool: "bash", Access: types.AccessBash, Command: "ls",
		AllowRules: []string{"Bash(*)"},
	})
	if d != workspace.Allow {
		t.Fatalf("Bash(*) must allow bash, got %s", d)
	}
}

func TestAskNeedsApproval(t *testing.T) {
	d, _ := workspace.Check(workspace.Request{Tool: "write_file", Access: types.AccessEdit, Path: "/tmp/x"})
	if d != workspace.Ask {
		t.Fatalf("edits ask, got %s", d)
	}
}

func TestAutoModeAllowsEditsButNotBash(t *testing.T) {
	d, _ := workspace.Check(workspace.Request{Tool: "write_file", Access: types.AccessEdit, Path: "/tmp/x", AutoMode: true})
	if d != workspace.Allow {
		t.Fatalf("auto allows edit, got %s", d)
	}
	d, _ = workspace.Check(workspace.Request{Tool: "bash", Access: types.AccessBash, Command: "ls", AutoMode: true})
	if d != workspace.Ask {
		t.Fatalf("auto still asks bash, got %s", d)
	}
}

func TestExtraRootAllowsEdit(t *testing.T) {
	root := t.TempDir()
	d, _ := workspace.Check(workspace.Request{
		Tool: "write_file", Access: types.AccessEdit,
		Path: filepath.Join(root, "a.go"), ExtraRoots: []string{root},
	})
	if d != workspace.Allow {
		t.Fatalf("extra root must allow, got %s", d)
	}
}

func TestSubagentGetsFreshInactivePlanTracker(t *testing.T) {
	parent := workspace.PlanTracker{}
	parent.Enter("/tmp/plan.md")
	child := workspace.NewInactive()
	if child.Active {
		t.Fatal("subagents inherit permissions but get a fresh Inactive plan tracker")
	}
	if !parent.Active {
		t.Fatal("parent plan tracker must stay active")
	}
}
