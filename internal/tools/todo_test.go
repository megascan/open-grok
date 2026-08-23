package tools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestTodoWriteReplaceAndMerge(t *testing.T) {
	dir := t.TempDir()
	tc := tools.Context{SessionDir: filepath.Join(dir, "sess")}
	replace, _ := json.Marshal(map[string]any{
		"merge": false,
		"todos": []map[string]any{
			{"id": "1", "content": "Explore codebase", "status": "in_progress"},
			{"id": "2", "content": "Review tools", "status": "pending"},
			{"id": "3", "content": "Write tests", "status": "pending"},
		},
	})
	res := tools.TodoWrite{}.Execute(context.Background(), tc, replace)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "[in_progress] 1: Explore codebase") {
		t.Fatalf("%s", res.Content)
	}

	// Status-only merge preserves content (Rust default merge=true when omitted).
	merge, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{
			{"id": "1", "status": "completed"},
			{"id": "2", "status": "in_progress"},
		},
	})
	res = tools.TodoWrite{}.Execute(context.Background(), tc, merge)
	if res.IsError {
		t.Fatal(res.Content)
	}
	got := tools.LoadTodos(tc.SessionDir)
	if len(got) != 3 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].Content != "Explore codebase" || got[0].Status != tools.TodoCompleted {
		t.Fatalf("%+v", got[0])
	}
	if got[1].Status != tools.TodoInProgress || got[1].Content != "Review tools" {
		t.Fatalf("%+v", got[1])
	}
	if got[2].Status != tools.TodoPending {
		t.Fatalf("%+v", got[2])
	}
}

func TestTodoWriteDuplicateIDsError(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{
			{"id": "a", "content": "one"},
			{"id": "a", "content": "two"},
		},
	})
	res := tools.TodoWrite{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "Duplicate Todo ID") {
		t.Fatalf("%+v", res)
	}
}

func TestTodoWriteAccessNone(t *testing.T) {
	if (tools.TodoWrite{}).Access() != types.AccessNone {
		t.Fatal("todo_write must not go through edit/bash gates")
	}
}

func TestTodoIconsMatchRustPane(t *testing.T) {
	if tools.TodoIcon(tools.TodoPending) != "□" {
		t.Fatal("pending")
	}
	if tools.TodoIcon(tools.TodoInProgress) != "▶" {
		t.Fatal("in_progress")
	}
	if tools.TodoIcon(tools.TodoCompleted) != "✓" {
		t.Fatal("completed")
	}
	if tools.TodoIcon(tools.TodoCancelled) != "✗" {
		t.Fatal("cancelled")
	}
}
