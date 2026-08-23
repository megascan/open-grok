// Package acp implements Agent Client Protocol JSON-RPC over stdio and in-process use.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type note struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Options configure the ACP agent.
type Options struct {
	CWD    string
	Model  string
	YOLO   bool
	Bearer string
	In     io.Reader
	Out    io.Writer
}

// ServeStdio runs JSON-RPC ACP on in/out until EOF.
func ServeStdio(ctx context.Context, opt Options) error {
	if opt.In == nil {
		opt.In = os.Stdin
	}
	if opt.Out == nil {
		opt.Out = os.Stdout
	}
	if opt.CWD == "" {
		opt.CWD, _ = os.Getwd()
	}
	s := &server{opt: opt, sessions: map[string]*agent.Session{}}
	sc := bufio.NewScanner(opt.In)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	enc := json.NewEncoder(opt.Out)
	var mu sync.Mutex
	write := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.Method == "" {
			continue
		}
		res, err := s.handle(ctx, req, write)
		if len(req.ID) > 0 {
			if err != nil {
				code := -32603
				if strings.HasPrefix(err.Error(), "unknown method") {
					code = -32601
				}
				write(rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{Code: code, Message: err.Error()}})
			} else {
				write(rpcResp{JSONRPC: "2.0", ID: req.ID, Result: res})
			}
		}
	}
	return sc.Err()
}

type server struct {
	opt      Options
	sessions map[string]*agent.Session
	mu       sync.Mutex
	seq      atomic.Int64
}

func (s *server) handle(ctx context.Context, req rpcReq, write func(any)) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": 1,
			"serverInfo": map[string]any{
				"name":    "open-grok",
				"version": version.String(),
			},
			"agentCapabilities": map[string]any{
				"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": true},
			},
		}, nil
	case "authenticate":
		return map[string]any{}, nil
	case "session/new":
		return s.sessionNew(req.Params)
	case "session/load":
		return s.sessionLoad(req.Params)
	case "session/prompt":
		return s.sessionPrompt(ctx, req.Params, write)
	case "session/cancel":
		return map[string]any{}, nil
	case "session/list":
		return s.sessionList()
	case "session/set_model":
		return s.sessionSetModel(req.Params)
	default:
		return nil, fmt.Errorf("unknown method %s", req.Method)
	}
}

func (s *server) sessionNew(params json.RawMessage) (any, error) {
	var p struct {
		CWD string `json:"cwd"`
	}
	_ = json.Unmarshal(params, &p)
	cwd := p.CWD
	if cwd == "" {
		cwd = s.opt.CWD
	}
	model, ok := catalog.Lookup(s.opt.Model)
	if !ok {
		model, _ = catalog.Lookup(catalog.DefaultID())
	}
	sess, err := agent.NewSession(cwd, model, s.opt.YOLO)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return map[string]any{"sessionId": sess.ID}, nil
}

func (s *server) sessionLoad(params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		CWD       string `json:"cwd"`
	}
	_ = json.Unmarshal(params, &p)
	cwd := p.CWD
	if cwd == "" {
		cwd = s.opt.CWD
	}
	sess, err := agent.LoadSession(cwd, p.SessionID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return map[string]any{"sessionId": sess.ID}, nil
}

func (s *server) sessionPrompt(ctx context.Context, params json.RawMessage, write func(any)) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	sess := s.sessions[p.SessionID]
	s.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("unknown session")
	}
	var text string
	for _, b := range p.Prompt {
		if b.Type == "text" || b.Type == "" {
			text += b.Text
		}
	}
	model, ok := catalog.Lookup(sess.ModelID)
	if !ok {
		model, _ = catalog.Lookup(catalog.DefaultID())
	}
	bearer := s.opt.Bearer
	if bearer == "" {
		kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
		var err error
		bearer, _, err = auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return nil, err
		}
	}
	sid := p.SessionID
	err := agent.RunTurn(ctx, sess, agent.Runtime{Model: model, Bearer: bearer}, text, func(ev agent.Event) {
		switch ev.Kind {
		case "text":
			write(note{JSONRPC: "2.0", Method: "session/update", Params: map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": ev.Text},
				},
			}})
		case "tool":
			write(note{JSONRPC: "2.0", Method: "session/update", Params: map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "tool_call",
					"toolCallId":    ev.ToolID,
					"title":         ev.Tool,
					"kind":          "execute",
					"status":        "pending",
				},
			}})
		case "tool_result":
			status := "completed"
			if ev.IsError {
				status = "failed"
			}
			write(note{JSONRPC: "2.0", Method: "session/update", Params: map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "tool_call_update",
					"toolCallId":    ev.ToolID,
					"status":        status,
					"content":       []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": ev.Text}}},
				},
			}})
		}
	})
	if err != nil {
		return map[string]any{"stopReason": "error"}, err
	}
	_ = uuid.Nil
	_ = types.RoleUser
	return map[string]any{"stopReason": "end_turn"}, nil
}

func (s *server) sessionList() (any, error) {
	list, err := agent.ListSessions(s.opt.CWD)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, sess := range list {
		rows = append(rows, map[string]any{
			"sessionId": sess.ID,
			"title":     sess.Title,
			"updatedAt": sess.UpdatedAt,
		})
	}
	return map[string]any{"sessions": rows}, nil
}

func (s *server) sessionSetModel(params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	sess := s.sessions[p.SessionID]
	s.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("unknown session")
	}
	model, ok := catalog.Lookup(p.ModelID)
	if !ok {
		return nil, fmt.Errorf("unknown model %s", p.ModelID)
	}
	sess.ModelID = model.ID
	sess.Provider = model.Provider
	sess.Backend = model.APIBackend
	sess.CloseExportBoundary(model.Provider)
	_ = sess.Persist()
	s.opt.Model = model.ID
	return map[string]any{"modelId": model.ID}, nil
}
