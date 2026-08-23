package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/megascan/open-grok/internal/mcp"
	"github.com/megascan/open-grok/internal/types"
)

// MCPTool is a live MCP tools/call wrapper. Access is MCP so YOLO still
// goes through the permission manager; plan mode does not treat it as Edit.
type MCPTool struct {
	Client     *mcp.Client
	ServerName string
	Spec       mcp.ToolSpec
}

func (t MCPTool) Name() string {
	return "mcp_" + sanitize(t.ServerName) + "_" + sanitize(t.Spec.Name)
}

func (t MCPTool) Description() string {
	d := t.Spec.Description
	if d == "" {
		d = "MCP tool " + t.Spec.Name + " from server " + t.ServerName
	}
	return d
}

func (MCPTool) Access() types.AccessKind { return types.AccessMCP }

func (t MCPTool) Parameters() map[string]any {
	if t.Spec.InputSchema != nil {
		return t.Spec.InputSchema
	}
	return objectSchema(map[string]any{}, nil)
}

func (t MCPTool) Execute(ctx context.Context, _ Context, args json.RawMessage) Result {
	if t.Client == nil {
		return errResult("mcp client is not connected")
	}
	out, isErr, err := t.Client.CallTool(ctx, t.Spec.Name, args)
	if err != nil {
		return errResult(err.Error())
	}
	if isErr {
		return errResult(out)
	}
	return okResult(out)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unnamed"
	}
	return out
}

// LoadMCPTools connects configured servers (fail-open) and returns wrappers.
func LoadMCPTools(ctx context.Context) ([]Tool, []*mcp.Client) {
	var tools []Tool
	var clients []*mcp.Client
	for _, s := range mcp.LoadServers() {
		c, err := mcp.Connect(ctx, s)
		if err != nil {
			continue
		}
		specs, err := c.ListTools(ctx)
		if err != nil {
			_ = c.Close()
			continue
		}
		clients = append(clients, c)
		for _, spec := range specs {
			tools = append(tools, MCPTool{Client: c, ServerName: s.Name, Spec: spec})
		}
	}
	return tools, clients
}
