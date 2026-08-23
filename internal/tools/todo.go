package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/megascan/open-grok/internal/types"
)

const todoFileName = "todos.json"

// Todo statuses match the Rust grok_build TodoWrite tool.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
	TodoCancelled  = "cancelled"
)

// TodoItem is one structured task the user sees live in the todo pane.
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type todoWriteArgs struct {
	Merge *bool            `json:"merge"`
	Todos []todoWriteEntry `json:"todos"`
}

type todoWriteEntry struct {
	ID      string  `json:"id"`
	Content *string `json:"content"`
	Status  *string `json:"status"`
}

var (
	todoMu    sync.Mutex
	todoCache = map[string][]TodoItem{}
)

// TodoWrite creates and updates the session task list. Access is none so the
// pane can update without a permission card; the user already sees the list.
type TodoWrite struct{}

func (TodoWrite) Name() string { return "todo_write" }
func (TodoWrite) Description() string {
	return "Create and manage a structured task list. The user sees this list live — it is your primary way to show progress.\n\nUse for any task with 3+ steps. Skip for trivial single-step work."
}
func (TodoWrite) Access() types.AccessKind { return types.AccessNone }
func (TodoWrite) Parameters() map[string]any {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string", "description": "Unique identifier for the todo item"},
			"content": map[string]any{"type": "string", "description": "The description/content of the todo item"},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{TodoPending, TodoInProgress, TodoCompleted, TodoCancelled},
				"description": "The status of the todo item: pending, in_progress, completed, or cancelled",
			},
		},
		"required": []string{"id"},
	}
	return objectSchema(map[string]any{
		"merge": map[string]any{
			"type":        "boolean",
			"description": "Optional. When true (default), merges the provided todos into the existing list by id — send only the items you are changing, and to flip status without changing content send just id + status. When false, the provided todos replace the existing list.",
		},
		"todos": map[string]any{
			"type":        "array",
			"description": "Array of todo items to write to the workspace",
			"items":       item,
		},
	}, []string{"todos"})
}

func (TodoWrite) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	var in todoWriteArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return errResult("invalid todo_write args: " + err.Error())
	}
	if err := validateTodoIDs(in.Todos); err != nil {
		return errResult(err.Error())
	}
	merge := true
	if in.Merge != nil {
		merge = *in.Merge
	}
	state := loadTodos(tc.SessionDir)
	if merge {
		state = applyTodoMerge(state, in.Todos)
	} else {
		state = applyTodoReplace(in.Todos)
	}
	if err := saveTodos(tc.SessionDir, state); err != nil {
		return errResult(err.Error())
	}
	return okResult(summarizeTodos(state))
}

func validateTodoIDs(updates []todoWriteEntry) error {
	seen := map[string]struct{}{}
	for _, u := range updates {
		id := strings.TrimSpace(u.ID)
		if id == "" {
			return fmt.Errorf("Missing Todo ID in mode: write")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("Duplicate Todo ID in response: %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func applyTodoReplace(updates []todoWriteEntry) []TodoItem {
	out := make([]TodoItem, 0, len(updates))
	for _, u := range updates {
		out = append(out, todoFromUpdate(u, nil))
	}
	return out
}

func applyTodoMerge(state []TodoItem, updates []todoWriteEntry) []TodoItem {
	idx := map[string]int{}
	for i, t := range state {
		idx[t.ID] = i
	}
	for _, u := range updates {
		id := strings.TrimSpace(u.ID)
		if i, ok := idx[id]; ok {
			if u.Content != nil && strings.TrimSpace(*u.Content) != "" {
				state[i].Content = *u.Content
			}
			if u.Status != nil && *u.Status != "" {
				state[i].Status = normalizeTodoStatus(*u.Status)
			}
			continue
		}
		item := todoFromUpdate(u, nil)
		idx[item.ID] = len(state)
		state = append(state, item)
	}
	return state
}

func todoFromUpdate(u todoWriteEntry, prev *TodoItem) TodoItem {
	id := strings.TrimSpace(u.ID)
	content := id
	if u.Content != nil && strings.TrimSpace(*u.Content) != "" {
		content = *u.Content
	} else if prev != nil && prev.Content != "" {
		content = prev.Content
	}
	status := TodoPending
	if u.Status != nil && *u.Status != "" {
		status = normalizeTodoStatus(*u.Status)
	} else if prev != nil && prev.Status != "" {
		status = prev.Status
	}
	return TodoItem{ID: id, Content: content, Status: status, Priority: "medium"}
}

func normalizeTodoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case TodoInProgress, "in-progress", "inprogress":
		return TodoInProgress
	case TodoCompleted, "complete", "done":
		return TodoCompleted
	case TodoCancelled, "canceled":
		return TodoCancelled
	default:
		return TodoPending
	}
}

func summarizeTodos(state []TodoItem) string {
	if len(state) == 0 {
		return "No tasks currently tracked."
	}
	var b strings.Builder
	for _, t := range state {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", t.Status, t.ID, t.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func todosPath(sessionDir string) string {
	if sessionDir == "" {
		return ""
	}
	return filepath.Join(sessionDir, todoFileName)
}

func loadTodos(sessionDir string) []TodoItem {
	if sessionDir == "" {
		return nil
	}
	todoMu.Lock()
	defer todoMu.Unlock()
	if cached, ok := todoCache[sessionDir]; ok {
		out := make([]TodoItem, len(cached))
		copy(out, cached)
		return out
	}
	data, err := os.ReadFile(todosPath(sessionDir))
	if err != nil {
		return nil
	}
	var items []TodoItem
	if json.Unmarshal(data, &items) != nil {
		return nil
	}
	todoCache[sessionDir] = items
	out := make([]TodoItem, len(items))
	copy(out, items)
	return out
}

func saveTodos(sessionDir string, items []TodoItem) error {
	if sessionDir == "" {
		return fmt.Errorf("todo_write requires a session directory")
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(todosPath(sessionDir), data, 0o600); err != nil {
		return err
	}
	todoMu.Lock()
	cloned := make([]TodoItem, len(items))
	copy(cloned, items)
	todoCache[sessionDir] = cloned
	todoMu.Unlock()
	return nil
}

// LoadTodos returns the persisted session todo list for the pager pane.
func LoadTodos(sessionDir string) []TodoItem {
	return loadTodos(sessionDir)
}

// TodoIcon is the pane prefix for a status (□ ▶ ✓ ✗).
func TodoIcon(status string) string {
	switch status {
	case TodoInProgress:
		return "▶"
	case TodoCompleted:
		return "✓"
	case TodoCancelled:
		return "✗"
	default:
		return "□"
	}
}
