// Package mcp is a JSON-RPC MCP client. It lists and calls tools over stdio
// or HTTP. Bridging is fail-open and must be combined with deny rules.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/megascan/open-grok/internal/config"
)

// Server is a configured MCP stdio or HTTP server.
type Server struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
}

// ToolSpec is one MCP tools/list entry.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client is a live MCP session (one child process or HTTP endpoint).
type Client struct {
	Server Server
	close  func() error
	call   func(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify func(ctx context.Context, method string, params any) error
}

// LoadServers reads [mcp_servers] from config.toml.
func LoadServers() []Server {
	cfg, err := config.Load()
	if err != nil || cfg.MCP == nil {
		return nil
	}
	var out []Server
	for name, s := range cfg.MCP {
		if s.Command == "" && s.URL == "" {
			continue
		}
		out = append(out, Server{Name: name, Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL})
	}
	return out
}

// Connect starts a stdio server or binds an HTTP endpoint, then initialize.
func Connect(ctx context.Context, s Server) (*Client, error) {
	if s.URL != "" {
		return connectHTTP(ctx, s)
	}
	if s.Command == "" {
		return nil, fmt.Errorf("mcp server %s has no command or url", s.Name)
	}
	return connectStdio(ctx, s)
}

// Close tears down the child process. HTTP clients are no-ops.
func (c *Client) Close() error {
	if c == nil || c.close == nil {
		return nil
	}
	return c.close()
}

// ListTools runs tools/list.
func (c *Client) ListTools(ctx context.Context) ([]ToolSpec, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Tools []ToolSpec `json:"tools"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	return wrap.Tools, nil
}

// CallTool runs tools/call and returns the concatenated text content.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	if !json.Valid(args) || len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var parsed any
	_ = json.Unmarshal(args, &parsed)
	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": parsed,
	})
	if err != nil {
		return "", true, err
	}
	var wrap struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return string(raw), false, nil
	}
	var b strings.Builder
	for _, c := range wrap.Content {
		b.WriteString(c.Text)
	}
	if b.Len() == 0 {
		return string(raw), wrap.IsError, nil
	}
	return b.String(), wrap.IsError, nil
}

func connectStdio(ctx context.Context, s Server) (*Client, error) {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tr := newTransport(stdout, stdin)
	c := &Client{
		Server: s,
		close: func() error {
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return cmd.Wait()
		},
		call:   tr.call,
		notify: tr.notify,
	}
	if err := initialize(ctx, c); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func connectHTTP(ctx context.Context, s Server) (*Client, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var id atomic.Int64
	c := &Client{
		Server: s,
		close:  func() error { return nil },
		notify: func(context.Context, string, any) error { return nil },
		call: func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			n := id.Add(1)
			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      n,
				"method":  method,
				"params":  params,
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			if err != nil {
				return nil, err
			}
			if resp.StatusCode >= 300 {
				return nil, fmt.Errorf("mcp http %s: HTTP %d: %s", s.Name, resp.StatusCode, truncate(string(data), 400))
			}
			return rpcResult(data)
		},
	}
	if err := initialize(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func initialize(ctx context.Context, c *Client) error {
	raw, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"roots": map[string]any{"listChanged": false}},
		"clientInfo":      map[string]any{"name": "entropy", "version": "go"},
	})
	if err != nil {
		return err
	}
	_ = raw
	if c.notify != nil {
		_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	}
	return nil
}

type transport struct {
	in   *bufio.Reader
	out  io.Writer
	mu   sync.Mutex
	id   atomic.Int64
	wait map[int64]chan rpcResp
}

func newTransport(r io.Reader, w io.Writer) *transport {
	t := &transport{
		in:   bufio.NewReader(r),
		out:  w,
		wait: map[int64]chan rpcResp{},
	}
	go t.readLoop()
	return t
}

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (t *transport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.id.Add(1)
	ch := make(chan rpcResp, 1)
	t.mu.Lock()
	t.wait[id] = ch
	t.mu.Unlock()
	if err := t.write(rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (t *transport) notify(_ context.Context, method string, params any) error {
	return t.write(rpcReq{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *transport) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(t.out, header); err != nil {
		return err
	}
	_, err = t.out.Write(data)
	return err
}

func (t *transport) readLoop() {
	for {
		frame, err := readFrame(t.in)
		if err != nil {
			return
		}
		var resp rpcResp
		if err := json.Unmarshal(frame, &resp); err != nil {
			continue
		}
		id, ok := asInt64(resp.ID)
		if !ok {
			continue
		}
		t.mu.Lock()
		ch := t.wait[id]
		delete(t.wait, id)
		t.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	// LSP-style headers, falling back to a single JSON line.
	var contentLen int
	sawHeader := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trim := strings.TrimSpace(line)
		if trim == "" {
			if sawHeader {
				break
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(trim), "content-length:") {
			n, _ := strconv.Atoi(strings.TrimSpace(trim[len("content-length:"):]))
			contentLen = n
			sawHeader = true
			continue
		}
		if strings.HasPrefix(trim, "{") {
			return []byte(trim), nil
		}
		sawHeader = true
	}
	if contentLen <= 0 {
		return nil, io.ErrUnexpectedEOF
	}
	buf := make([]byte, contentLen)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func rpcResult(data []byte) (json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("event:")) || bytes.Contains(data, []byte("\ndata:")) {
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if bytes.HasPrefix(line, []byte("data:")) {
				data = bytes.TrimSpace(line[5:])
				break
			}
		}
	}
	var resp rpcResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp: %s", resp.Error.Message)
	}
	if len(resp.Result) > 0 {
		return resp.Result, nil
	}
	return data, nil
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Probe starts a server long enough to send initialize. Failures never
// escalate into a security boundary.
func Probe(ctx context.Context, s Server) error {
	c, err := Connect(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.ListTools(ctx)
	return err
}
