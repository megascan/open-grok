package tools

import (
	"context"
	"encoding/json"

	"github.com/megascan/open-grok/internal/types"
)

// UpdateGoal is the model-facing progress tool. Registered even when no
// /goal is running so a call outside harness rejects cleanly instead of
// looking like an unknown tool.
type UpdateGoal struct{}

func (UpdateGoal) Name() string { return "update_goal" }
func (UpdateGoal) Description() string {
	return "Report goal progress. Use message for a short progress note. Set completed=true ONLY when the objective is fully achieved, together with a completion summary — that records a claim; it does not end goal mode by itself. Set blocked_reason only after 3+ consecutive failed attempts at the same problem (never success text)."
}
func (UpdateGoal) Access() types.AccessKind { return types.AccessNone }
func (UpdateGoal) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"completed": map[string]any{
			"type":        "boolean",
			"description": "Set to true ONLY when the goal is fully achieved. Use together with message for a completion summary. This records a claim; the user confirms with /goal complete.",
		},
		"message": map[string]any{
			"type":        "string",
			"description": "Short progress note (or completion summary when completed is true).",
		},
		"blocked_reason": map[string]any{
			"type":        "string",
			"description": "Set only when truly stuck after 3+ consecutive failed attempts. This is a FAILURE signal.",
		},
	}, nil)
}

func (UpdateGoal) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	summary, code, err := tc.Goal.ApplyUpdate(asBool(m["completed"]), str(m["message"]), str(m["blocked_reason"]))
	if err != nil {
		msg := err.Error()
		if code != "" {
			msg = code + ": " + msg
		}
		return errResult(msg)
	}
	return okResult(summary)
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "yes"
	case float64:
		return t != 0
	default:
		return false
	}
}
