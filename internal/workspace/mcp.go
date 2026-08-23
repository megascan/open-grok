package workspace

import (
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

// MCPToolNameDelimiter is the Rust qualified MCP id separator (`linear__list_issues`).
const MCPToolNameDelimiter = "__"

// MCPServerAllowPrefix is persisted for "Always allow: all tools from Server".
const MCPServerAllowPrefix = "mcp:"

// MCPTitleizeSegment pretty-formats one MCP server or action segment:
// `list_issues` → `List Issues`. CamelCase and hyphens stay intact.
func MCPTitleizeSegment(name string) string {
	parts := strings.Split(name, "_")
	out := make([]string, 0, len(parts))
	for _, word := range parts {
		if word == "" {
			continue
		}
		r := []rune(word)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		out = append(out, string(r))
	}
	return strings.Join(out, " ")
}

// MCPToolDisplayName is the permission-card label `(Linear) List Issues`.
func MCPToolDisplayName(toolName, serverPrefix string) string {
	action := toolName
	if serverPrefix != "" {
		prefixed := serverPrefix + MCPToolNameDelimiter
		if strings.HasPrefix(toolName, prefixed) {
			action = strings.TrimPrefix(toolName, prefixed)
		}
	}
	if serverPrefix != "" {
		return "(" + MCPTitleizeSegment(serverPrefix) + ") " + MCPTitleizeSegment(action)
	}
	return MCPTitleizeSegment(toolName)
}

// MCPServerAllowRule persists a server-wide always-allow grant.
func MCPServerAllowRule(server string) string {
	return MCPServerAllowPrefix + strings.TrimSpace(server)
}

func matchMCPAllow(rule string, req Request) bool {
	if req.Access != types.AccessMCP {
		return false
	}
	if !strings.HasPrefix(rule, MCPServerAllowPrefix) {
		return false
	}
	server := strings.TrimPrefix(rule, MCPServerAllowPrefix)
	if server == "" {
		return false
	}
	if strings.HasPrefix(req.Tool, server+MCPToolNameDelimiter) {
		return true
	}
	goPrefix := "mcp_" + sanitizeMCPIdent(server) + "_"
	return strings.HasPrefix(req.Tool, goPrefix)
}

func sanitizeMCPIdent(s string) string {
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
