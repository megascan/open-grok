package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

// AskUserJSONPrefix marks a multi-question payload stuffed through AskUser.
const AskUserJSONPrefix = "ASK_USER_JSON:"

// UserQuestion is one AskUserQuestion item (Rust Question).
type UserQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Multi    bool     `json:"multi_select,omitempty"`
}

// AskUserQuestion surfaces a question to the human (ACP reverse-RPC in full
// clients; here it returns the prompt so the client can answer).
type AskUserQuestion struct{}

func (AskUserQuestion) Name() string { return "ask_user_question" }
func (AskUserQuestion) Description() string {
	return "Ask the user a question. Direct-only: remains top-level in Code Mode Only."
}
func (AskUserQuestion) Access() types.AccessKind { return types.AccessNone }
func (AskUserQuestion) Parameters() map[string]any {
	optionObj := objectSchema(map[string]any{
		"label":       map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
	}, []string{"label"})
	questionObj := objectSchema(map[string]any{
		"question":     map[string]any{"type": "string"},
		"options":      map[string]any{"type": "array", "items": optionObj},
		"multi_select": map[string]any{"type": "boolean"},
	}, []string{"question", "options"})
	return objectSchema(map[string]any{
		"question":  map[string]any{"type": "string"},
		"options":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"questions": map[string]any{"type": "array", "items": questionObj},
	}, nil)
}

func (AskUserQuestion) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	qs := parseUserQuestions(m)
	if len(qs) == 0 {
		return errResult("ask_user_question requires question or questions")
	}
	if tc.AskUser == nil {
		return okResult("USER_QUESTION: " + qs[0].Question)
	}
	if len(qs) == 1 {
		return okResult(tc.AskUser(qs[0].Question, qs[0].Options))
	}
	body, _ := json.Marshal(qs)
	return okResult(tc.AskUser(AskUserJSONPrefix+string(body), nil))
}

func parseUserQuestions(m map[string]any) []UserQuestion {
	if raw, ok := m["questions"].([]any); ok && len(raw) > 0 {
		var out []UserQuestion
		for _, item := range raw {
			om, _ := item.(map[string]any)
			if om == nil {
				continue
			}
			q := UserQuestion{Question: str(om["question"]), Multi: boolVal(om["multi_select"]) || boolVal(om["multiSelect"])}
			q.Options = optionLabels(om["options"])
			if q.Question == "" {
				continue
			}
			out = append(out, q)
		}
		if len(out) > 0 {
			return out
		}
	}
	q := str(m["question"])
	if q == "" {
		return nil
	}
	return []UserQuestion{{Question: q, Options: optionLabels(m["options"])}}
}

func optionLabels(raw any) []string {
	arr, _ := raw.([]any)
	var opts []string
	for _, o := range arr {
		switch v := o.(type) {
		case string:
			opts = append(opts, v)
		case map[string]any:
			if s := str(v["label"]); s != "" {
				opts = append(opts, s)
			} else if s := str(v["id"]); s != "" {
				opts = append(opts, s)
			}
		}
	}
	return opts
}

func boolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

// DecodeAskUserJSON unpacks a multi-question payload sent through AskUser.
func DecodeAskUserJSON(q string) ([]UserQuestion, bool) {
	rest, ok := strings.CutPrefix(q, AskUserJSONPrefix)
	if !ok {
		return nil, false
	}
	var qs []UserQuestion
	if json.Unmarshal([]byte(rest), &qs) != nil || len(qs) == 0 {
		return nil, false
	}
	return qs, true
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
