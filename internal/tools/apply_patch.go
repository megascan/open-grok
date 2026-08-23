package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

const (
	beginPatch = "*** Begin Patch"
	endPatch   = "*** End Patch"
	addFile    = "*** Add File: "
	deleteFile = "*** Delete File: "
	updateFile = "*** Update File: "
	moveTo     = "*** Move to: "
)

// ApplyPatch applies a Codex apply-patch document (not unified diff).
type ApplyPatch struct{}

func (ApplyPatch) Name() string { return "apply_patch" }
func (ApplyPatch) Description() string {
	return "Apply a Codex apply-patch document (*** Begin Patch … *** End Patch) with Add/Delete/Update File hunks."
}
func (ApplyPatch) Access() types.AccessKind { return types.AccessEdit }
func (ApplyPatch) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"patch": map[string]any{"type": "string", "description": "Full apply-patch document"},
		"path":  map[string]any{"type": "string", "description": "Unused; paths live in the patch"},
	}, []string{"patch"})
}

func (ApplyPatch) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	patch := str(m["patch"])
	if patch == "" {
		patch = str(m["input"])
	}
	hunks, err := ParseApplyPatch(patch)
	if err != nil {
		return errResult(err.Error())
	}
	var done []string
	for _, h := range hunks {
		p, err := resolvePath(tc.CWD, h.Path)
		if err != nil {
			return errResult(err.Error())
		}
		switch h.Kind {
		case hunkAdd:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return errResult(err.Error())
			}
			if err := os.WriteFile(p, []byte(h.Contents), 0o644); err != nil {
				return errResult(err.Error())
			}
			recordAgentWrite(tc, p)
			done = append(done, "add "+p)
		case hunkDelete:
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return errResult(err.Error())
			}
			recordAgentWrite(tc, p)
			done = append(done, "delete "+p)
		case hunkUpdate:
			data, err := os.ReadFile(p)
			if err != nil {
				return errResult(err.Error())
			}
			updated, err := applyUpdate(string(data), h)
			if err != nil {
				return errResult(p + ": " + err.Error())
			}
			dest := p
			if h.MoveTo != "" {
				dest, err = resolvePath(tc.CWD, h.MoveTo)
				if err != nil {
					return errResult(err.Error())
				}
				_ = os.MkdirAll(filepath.Dir(dest), 0o755)
				_ = os.Remove(p)
			}
			if err := os.WriteFile(dest, []byte(updated), 0o644); err != nil {
				return errResult(err.Error())
			}
			recordAgentWrite(tc, dest)
			done = append(done, "update "+dest)
		}
	}
	return okResult(strings.Join(done, "\n"))
}

type hunkKind int

const (
	hunkAdd hunkKind = iota
	hunkDelete
	hunkUpdate
)

// PatchHunk is one parsed apply-patch operation.
type PatchHunk struct {
	Kind     hunkKind
	Path     string
	MoveTo   string
	Contents string
	OldBlock string
	NewBlock string
}

// ParseApplyPatch parses a Codex apply-patch document.
func ParseApplyPatch(patch string) ([]PatchHunk, error) {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) >= 4 && (lines[0] == "<<EOF" || lines[0] == "<<'EOF'" || lines[0] == "<<\"EOF\"") {
		lines = lines[1 : len(lines)-1]
	}
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != beginPatch || strings.TrimSpace(lines[len(lines)-1]) != endPatch {
		return nil, fmt.Errorf("patch must start with %q and end with %q", beginPatch, endPatch)
	}
	var hunks []PatchHunk
	i := 1
	for i < len(lines)-1 {
		line := strings.TrimRight(lines[i], "\r")
		switch {
		case strings.HasPrefix(line, addFile):
			h := PatchHunk{Kind: hunkAdd, Path: strings.TrimPrefix(line, addFile)}
			i++
			var b strings.Builder
			for i < len(lines)-1 && strings.HasPrefix(lines[i], "+") {
				b.WriteString(lines[i][1:])
				b.WriteByte('\n')
				i++
			}
			h.Contents = b.String()
			hunks = append(hunks, h)
		case strings.HasPrefix(line, deleteFile):
			hunks = append(hunks, PatchHunk{Kind: hunkDelete, Path: strings.TrimPrefix(line, deleteFile)})
			i++
		case strings.HasPrefix(line, updateFile):
			h := PatchHunk{Kind: hunkUpdate, Path: strings.TrimPrefix(line, updateFile)}
			i++
			if i < len(lines)-1 && strings.HasPrefix(lines[i], moveTo) {
				h.MoveTo = strings.TrimPrefix(lines[i], moveTo)
				i++
			}
			var oldB, newB strings.Builder
			for i < len(lines)-1 {
				l := lines[i]
				if strings.HasPrefix(l, "*** ") && !strings.HasPrefix(l, "*** End of File") {
					break
				}
				if strings.HasPrefix(l, "@@") || l == "*** End of File" {
					i++
					continue
				}
				if len(l) == 0 {
					i++
					continue
				}
				switch l[0] {
				case ' ':
					oldB.WriteString(l[1:])
					oldB.WriteByte('\n')
					newB.WriteString(l[1:])
					newB.WriteByte('\n')
				case '-':
					oldB.WriteString(l[1:])
					oldB.WriteByte('\n')
				case '+':
					newB.WriteString(l[1:])
					newB.WriteByte('\n')
				}
				i++
			}
			h.OldBlock = strings.TrimSuffix(oldB.String(), "\n")
			h.NewBlock = strings.TrimSuffix(newB.String(), "\n")
			hunks = append(hunks, h)
		default:
			return nil, fmt.Errorf("unexpected patch line: %s", line)
		}
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("patch contained no hunks")
	}
	return hunks, nil
}

func applyUpdate(src string, h PatchHunk) (string, error) {
	if h.OldBlock == "" {
		if !strings.HasSuffix(src, "\n") && h.NewBlock != "" {
			src += "\n"
		}
		return src + h.NewBlock, nil
	}
	if !strings.Contains(src, h.OldBlock) {
		return "", fmt.Errorf("update context not found")
	}
	return strings.Replace(src, h.OldBlock, h.NewBlock, 1), nil
}
