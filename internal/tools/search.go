package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

// Grep searches file contents with a regular expression.
type Grep struct{}

func (Grep) Name() string { return "grep" }
func (Grep) Description() string {
	return "Search the workspace with a regular expression. Optional glob limits the files considered."
}
func (Grep) Access() types.AccessKind { return types.AccessRead }
func (Grep) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
		"glob":    map[string]any{"type": "string"},
		"head":    map[string]any{"type": "integer"},
	}, []string{"pattern"})
}

func (Grep) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	pat := str(m["pattern"])
	re, err := regexp.Compile(pat)
	if err != nil {
		return errResult("invalid regexp: " + err.Error())
	}
	root := tc.CWD
	if p := str(m["path"]); p != "" {
		root, err = resolvePath(tc.CWD, p)
		if err != nil {
			return errResult(err.Error())
		}
	}
	glob := str(m["glob"])
	head := 200
	if n, ok := m["head"].(float64); ok && n > 0 {
		head = int(n)
	}
	var hits []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			ok, _ := filepath.Match(glob, filepath.Base(path))
			if !ok {
				return nil
			}
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			if re.FindIndex(sc.Bytes()) == nil {
				continue
			}
			rel := path
			if r, err := filepath.Rel(tc.CWD, path); err == nil {
				rel = r
			}
			hits = append(hits, rel+":"+itoa(lineNo)+":"+sc.Text())
			if len(hits) >= head {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return okResult("no matches")
	}
	return okResult(strings.Join(hits, "\n"))
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "target", "vendor", ".opengrok", "dist":
		return true
	default:
		return false
	}
}

// Glob lists files matching a glob pattern.
type Glob struct{}

func (Glob) Name() string { return "glob" }
func (Glob) Description() string {
	return "Find files by glob pattern relative to the workspace (e.g. **/*.go)."
}
func (Glob) Access() types.AccessKind { return types.AccessRead }
func (Glob) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
	}, []string{"pattern"})
}

func (Glob) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	pattern := str(m["pattern"])
	root := tc.CWD
	if p := str(m["path"]); p != "" {
		var err error
		root, err = resolvePath(tc.CWD, p)
		if err != nil {
			return errResult(err.Error())
		}
	}
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		ok := false
		if matched, _ := filepath.Match(pattern, rel); matched {
			ok = true
		}
		if !ok {
			if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
				ok = true
			}
		}
		if !ok && strings.Contains(pattern, "**") {
			suffix := strings.TrimPrefix(pattern, "**/")
			if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
				ok = true
			}
		}
		if ok {
			matches = append(matches, rel)
			if len(matches) >= 500 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(matches) == 0 {
		return okResult("no files matched")
	}
	return okResult(strings.Join(matches, "\n"))
}

// ListDir lists a directory.
type ListDir struct{}

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Description() string {
	return "List files and directories at a path."
}
func (ListDir) Access() types.AccessKind { return types.AccessRead }
func (ListDir) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path": map[string]any{"type": "string"},
	}, []string{})
}

func (ListDir) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	p := str(m["path"])
	if p == "" {
		p = tc.CWD
	}
	path, err := resolvePath(tc.CWD, p)
	if err != nil {
		return errResult(err.Error())
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errResult(err.Error())
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			b.WriteString("dir  ")
		} else {
			b.WriteString("file ")
		}
		b.WriteString(e.Name())
		b.WriteByte('\n')
	}
	return okResult(strings.TrimRight(b.String(), "\n"))
}
