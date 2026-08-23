package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/megascan/open-grok/internal/types"
)

const maxReadBytes = 512 * 1024

// ReadFile reads a utf-8 text file, optionally sliced by line.
type ReadFile struct{}

func (ReadFile) Name() string { return "read_file" }
func (ReadFile) Description() string {
	return "Read a file from the workspace. Optional offset/limit are 1-based line numbers."
}
func (ReadFile) Access() types.AccessKind { return types.AccessRead }
func (ReadFile) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":   map[string]any{"type": "string"},
		"offset": map[string]any{"type": "integer"},
		"limit":  map[string]any{"type": "integer"},
	}, []string{"path"})
}

func (ReadFile) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	path, err := resolvePath(tc.CWD, str(m["path"]))
	if err != nil {
		return errResult(err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(err.Error())
	}
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}
	if !utf8.Valid(data) {
		return errResult("file is not valid UTF-8; use bash for binary inspection")
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	offset := 1
	if n, ok := m["offset"].(float64); ok && n > 0 {
		offset = int(n)
	}
	limit := len(lines)
	if n, ok := m["limit"].(float64); ok && n > 0 {
		limit = int(n)
	}
	if offset > len(lines) {
		return errResult("offset past end of file")
	}
	end := offset - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i, line := range lines[offset-1 : end] {
		fmt.Fprintf(&b, "%6d|%s\n", offset+i, line)
	}
	return okResult(strings.TrimRight(b.String(), "\n"))
}

// WriteFile creates or overwrites a utf-8 file. Callers must already have
// passed the plan-mode edit gate and permission manager.
type WriteFile struct{}

func (WriteFile) Name() string { return "write_file" }
func (WriteFile) Description() string {
	return "Create or overwrite a file with the given contents."
}
func (WriteFile) Access() types.AccessKind { return types.AccessEdit }
func (WriteFile) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":     map[string]any{"type": "string"},
		"contents": map[string]any{"type": "string"},
	}, []string{"path", "contents"})
}

func (WriteFile) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	path, err := resolvePath(tc.CWD, str(m["path"]))
	if err != nil {
		return errResult(err.Error())
	}
	contents := str(m["contents"])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errResult(err.Error())
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return errResult(err.Error())
	}
	recordAgentWrite(tc, path)
	return okResult("wrote " + path)
}

func resolvePath(cwd, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p), nil
}
