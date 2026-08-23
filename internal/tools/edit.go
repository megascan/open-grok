package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

// SearchReplace performs a single unique string replacement in a file.
type SearchReplace struct{}

func (SearchReplace) Name() string { return "search_replace" }
func (SearchReplace) Description() string {
	return "Replace exactly one occurrence of old_string with new_string in a file. old_string must be unique."
}
func (SearchReplace) Access() types.AccessKind { return types.AccessEdit }
func (SearchReplace) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":        map[string]any{"type": "string"},
		"old_string":  map[string]any{"type": "string"},
		"new_string":  map[string]any{"type": "string"},
		"replace_all": map[string]any{"type": "boolean"},
	}, []string{"path", "old_string", "new_string"})
}

func (SearchReplace) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	path, err := resolvePath(tc.CWD, str(m["path"]))
	if err != nil {
		return errResult(err.Error())
	}
	oldS := str(m["old_string"])
	newS := str(m["new_string"])
	if oldS == "" {
		return errResult("old_string is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(err.Error())
	}
	text := string(data)
	replaceAll, _ := m["replace_all"].(bool)
	count := strings.Count(text, oldS)
	if count == 0 {
		return errResult("old_string not found")
	}
	if count > 1 && !replaceAll {
		return errResult("old_string is not unique; pass replace_all or more context")
	}
	updated := strings.Replace(text, oldS, newS, -1)
	if !replaceAll {
		updated = strings.Replace(text, oldS, newS, 1)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return errResult(err.Error())
	}
	recordAgentWrite(tc, path)
	n := 1
	if replaceAll {
		n = count
	}
	return okResult("updated " + path + " (" + itoa(n) + " replacement(s))")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
