package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

// MemorySearch reads cross-session notes from ~/.entropy/memory.
type MemorySearch struct{}

func (MemorySearch) Name() string { return "memory_search" }
func (MemorySearch) Description() string {
	return "Search persisted Entropy memory notes under ~/.entropy/memory."
}
func (MemorySearch) Access() types.AccessKind { return types.AccessRead }
func (MemorySearch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"query": map[string]any{"type": "string"},
	}, []string{"query"})
}

func (MemorySearch) Execute(_ context.Context, _ Context, args json.RawMessage) Result {
	q := str(unmarshal(args)["query"])
	dir := filepath.Join(config.Home(), "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return okResult("no memory notes")
	}
	var hits []byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if q == "" || containsFold(string(data), q) || containsFold(e.Name(), q) {
			hits = append(hits, []byte("## "+e.Name()+"\n")...)
			hits = append(hits, data...)
			hits = append(hits, '\n')
		}
	}
	if len(hits) == 0 {
		return okResult("no matching memory")
	}
	return okResult(capOutput(string(hits), 80_000))
}

// MemorySave appends a note to ~/.entropy/memory.
type MemorySave struct{}

func (MemorySave) Name() string { return "memory_save" }
func (MemorySave) Description() string {
	return "Save a short note to ~/.entropy/memory for later sessions."
}
func (MemorySave) Access() types.AccessKind { return types.AccessNone }
func (MemorySave) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"name":    map[string]any{"type": "string"},
		"content": map[string]any{"type": "string"},
	}, []string{"name", "content"})
}

func (MemorySave) Execute(_ context.Context, _ Context, args json.RawMessage) Result {
	m := unmarshal(args)
	name := filepath.Base(str(m["name"]))
	if name == "" || name == "." {
		return errResult("name is required")
	}
	dir := filepath.Join(config.Home(), "memory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errResult(err.Error())
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(str(m["content"])), 0o600); err != nil {
		return errResult(err.Error())
	}
	return okResult("saved " + path)
}

func containsFold(hay, needle string) bool {
	return len(needle) == 0 || stringsContainsFold(hay, needle)
}

func stringsContainsFold(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		indexFold(s, substr) >= 0)
}

func indexFold(s, substr string) int {
	ls, lsub := len(s), len(substr)
	for i := 0; i+lsub <= ls; i++ {
		if equalFoldASCII(s[i:i+lsub], substr) {
			return i
		}
	}
	return -1
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
