package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/megascan/open-grok/internal/types"
)

var (
	hunkMu    sync.Mutex
	agentWrites = map[string]struct{}{}
)

// recordAgentWrite marks a path as an agent edit so hunk attribution is not
// "External". fsnotify alone is not sufficient.
func recordAgentWrite(tc Context, path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	hunkMu.Lock()
	agentWrites[abs] = struct{}{}
	hunkMu.Unlock()
	if tc.SessionDir == "" {
		return
	}
	_ = os.MkdirAll(tc.SessionDir, 0o700)
	log := filepath.Join(tc.SessionDir, "agent_writes.log")
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(abs + "\n")
}

// AgentWrote reports whether the agent (not an external editor) wrote path.
func AgentWrote(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	hunkMu.Lock()
	defer hunkMu.Unlock()
	_, ok := agentWrites[abs]
	return ok
}

// EnterPlanMode / ExitPlanMode toggle the session plan tracker.
type EnterPlanMode struct{}

func (EnterPlanMode) Name() string { return "enter_plan_mode" }
func (EnterPlanMode) Description() string {
	return "Switch into plan mode. After this, only the session plan.md file may be edited."
}
func (EnterPlanMode) Access() types.AccessKind { return types.AccessNone }
func (EnterPlanMode) Parameters() map[string]any {
	return objectSchema(map[string]any{}, nil)
}
func (EnterPlanMode) Execute(_ context.Context, tc Context, _ json.RawMessage) Result {
	return okResult("plan mode active; only " + tc.PlanFile + " may be edited")
}

// ExitPlanMode leaves plan mode after the user confirms the plan.
type ExitPlanMode struct{}

func (ExitPlanMode) Name() string { return "exit_plan_mode" }
func (ExitPlanMode) Description() string {
	return "Leave plan mode and resume normal editing after the plan is accepted."
}
func (ExitPlanMode) Access() types.AccessKind { return types.AccessNone }
func (ExitPlanMode) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"summary": map[string]any{"type": "string"},
	}, []string{})
}
func (ExitPlanMode) Execute(_ context.Context, _ Context, args json.RawMessage) Result {
	m := unmarshal(args)
	s := str(m["summary"])
	if s == "" {
		s = "plan accepted"
	}
	return okResult(s)
}

// DefaultPack is the Grok Build toolset used for Direct tool mode.
func DefaultPack() []Tool {
	return []Tool{
		Bash{},
		ReadFile{},
		WriteFile{},
		SearchReplace{},
		Grep{},
		Glob{},
		ListDir{},
		WebSearch{},
		WebFetch{},
		EnterPlanMode{},
		ExitPlanMode{},
	}
}

// Lookup finds a tool by model-facing name.
func Lookup(pack []Tool, name string) Tool {
	for _, t := range pack {
		if t.Name() == name {
			return t
		}
	}
	return nil
}
