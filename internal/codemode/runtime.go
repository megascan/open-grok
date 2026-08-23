// Package codemode is a persistent JavaScript session for Code Mode / Code Mode Only.
//
// The runtime uses dop251/goja (pure Go) so exec/wait works on every GOOS/GOARCH
// without CGO or a per-call process. Nested tools.* calls re-enter the same
// prepare/dispatch path as top-level tools. exec/wait are transport, not user-facing
// tool cards — callers should surface nested tools.* instead.
package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

// Runtime is one persistent JS session. Reset on rewind / incompatible route.
type Runtime struct {
	mu    sync.Mutex
	vm    *goja.Runtime
	cells map[string]*cell
	host  tools.Context
	pack  []tools.Tool
}

type cell struct {
	id     string
	result string
	err    string
	done   bool
}

// New builds a VM with tools.* bound to pack.
func New(host tools.Context, pack []tools.Tool) *Runtime {
	r := &Runtime{vm: goja.New(), cells: map[string]*cell{}, host: host, pack: pack}
	r.bindTools()
	return r
}

func (r *Runtime) bindTools() {
	obj := r.vm.NewObject()
	for _, t := range r.pack {
		t := t
		_ = obj.Set(t.Name(), func(call goja.FunctionCall) goja.Value {
			var raw json.RawMessage
			if len(call.Arguments) > 0 {
				exported := call.Arguments[0].Export()
				b, _ := json.Marshal(exported)
				raw = b
			} else {
				raw = json.RawMessage(`{}`)
			}
			if r.host.DispatchNested != nil {
				res := r.host.DispatchNested(context.Background(), t.Name(), raw)
				if res.IsError {
					panic(r.vm.ToValue(res.Content))
				}
				return r.vm.ToValue(res.Content)
			}
			res := t.Execute(context.Background(), r.host, raw)
			if res.IsError {
				panic(r.vm.ToValue(res.Content))
			}
			return r.vm.ToValue(res.Content)
		})
	}
	_ = r.vm.Set("tools", obj)
	_ = r.vm.Set("notify", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})
}

// Exec runs source in the persistent VM. Yields a cell_id when the script
// returns a thenable; otherwise returns the completion string.
func (r *Runtime) Exec(ctx context.Context, source string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if source == "" {
		return "", fmt.Errorf("exec: empty source")
	}
	v, err := r.vm.RunString(source)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	c := &cell{id: id, done: true}
	if v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		c.result = v.String()
	}
	r.cells[id] = c
	return c.result, nil
}

// Wait resumes or terminates a cell.
func (r *Runtime) Wait(_ context.Context, cellID string, terminate bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cells[cellID]
	if !ok {
		return "", fmt.Errorf("unknown cell_id %s", cellID)
	}
	if terminate {
		c.done = true
		c.result = "terminated"
	}
	return c.result, nil
}

// ExecTool is the model-facing exec function (xAI/DeepSeek envelope: {source}).
type ExecTool struct{ RT *Runtime }

func (ExecTool) Name() string { return "exec" }
func (ExecTool) Description() string {
	return "Run JavaScript in the persistent Code Mode session. Nested tools are available as tools.*."
}
func (ExecTool) Access() types.AccessKind { return types.AccessNone }
func (ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{"type": "string", "description": "Raw JavaScript"},
		},
		"required": []string{"source"},
	}
}

func (e ExecTool) Execute(ctx context.Context, _ tools.Context, args json.RawMessage) tools.Result {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	source, _ := m["source"].(string)
	if source == "" {
		source, _ = m["code"].(string)
	}
	out, err := e.RT.Exec(ctx, source)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if out == "" {
		out = "(ok)"
	}
	return tools.Result{Content: out}
}

// WaitTool is the model-facing wait function.
type WaitTool struct{ RT *Runtime }

func (WaitTool) Name() string { return "wait" }
func (WaitTool) Description() string {
	return "Wait for or terminate a yielded Code Mode cell by cell_id."
}
func (WaitTool) Access() types.AccessKind { return types.AccessNone }
func (WaitTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cell_id":       map[string]any{"type": "string"},
			"terminate":     map[string]any{"type": "boolean"},
			"yield_time_ms": map[string]any{"type": "integer"},
		},
		"required": []string{"cell_id"},
	}
}

func (w WaitTool) Execute(ctx context.Context, _ tools.Context, args json.RawMessage) tools.Result {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	id, _ := m["cell_id"].(string)
	term, _ := m["terminate"].(bool)
	out, err := w.RT.Wait(ctx, id, term)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: out}
}
