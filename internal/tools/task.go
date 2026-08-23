package tools

import (
	"context"
	"encoding/json"

	"github.com/megascan/open-grok/internal/types"
)

// AskUserQuestion surfaces a question to the human (ACP reverse-RPC in full
// clients; here it returns the prompt so the client can answer).
type AskUserQuestion struct{}

func (AskUserQuestion) Name() string { return "ask_user_question" }
func (AskUserQuestion) Description() string {
	return "Ask the user a question. Direct-only: remains top-level in Code Mode Only."
}
func (AskUserQuestion) Access() types.AccessKind { return types.AccessNone }
func (AskUserQuestion) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"question": map[string]any{"type": "string"},
		"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, []string{"question"})
}

func (AskUserQuestion) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	q := str(m["question"])
	var opts []string
	if raw, ok := m["options"].([]any); ok {
		for _, o := range raw {
			opts = append(opts, str(o))
		}
	}
	if tc.AskUser != nil {
		return okResult(tc.AskUser(q, opts))
	}
	return okResult("USER_QUESTION: " + q)
}

// TaskSpawn starts a depth-1 subagent. Nested spawns are rejected.
type TaskSpawn struct {
	// Spawn is set by the agent runtime; nil means the tool reports unsupported.
	Spawn func(ctx context.Context, prompt, label string) Result
}

func (t TaskSpawn) Name() string { return "task" }
func (TaskSpawn) Description() string {
	return "Spawn a subagent (max depth 1) with a fresh Inactive plan tracker. The child inherits YOLO/permissions."
}
func (TaskSpawn) Access() types.AccessKind { return types.AccessNone }
func (TaskSpawn) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt":      map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
	}, []string{"prompt"})
}

func (t TaskSpawn) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	if tc.SubagentDepth >= 1 {
		return errResult("nested subagents are unsupported (MAX_SUBAGENT_DEPTH = 1)")
	}
	if t.Spawn == nil {
		return errResult("task tool is not bound in this session")
	}
	m := unmarshal(args)
	return t.Spawn(ctx, str(m["prompt"]), str(m["description"]))
}
