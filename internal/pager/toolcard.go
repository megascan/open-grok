package pager

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	userCollapsedMaxLines = 3
	toolResultFirstLines  = 8
	toolResultLastLines   = 4
)

type verbKind int

const (
	verbStandalone verbKind = iota
	verbFile
	verbSkill
	verbSearch
	verbDir
	verbWebFetch
	verbWebSearch
	verbMemory
	verbMCPSearch
	verbCommand
	verbEdit
	verbMCP
	verbOther
)

func (k verbKind) groupable() bool {
	switch k {
	case verbFile, verbSkill, verbSearch, verbDir, verbWebFetch, verbWebSearch, verbMemory, verbMCPSearch:
		return true
	default:
		return false
	}
}

func (k verbKind) verb(running bool) string {
	past, present := "Used", "Using"
	switch k {
	case verbFile, verbSkill:
		past, present = "Read", "Reading"
	case verbSearch, verbWebSearch, verbMemory, verbMCPSearch:
		past, present = "Searched", "Searching"
	case verbDir:
		past, present = "Listed", "Listing"
	case verbWebFetch:
		past, present = "Fetched", "Fetching"
	case verbCommand, verbOther:
		past, present = "Ran", "Running"
	case verbEdit:
		past, present = "Edited", "Editing"
	case verbMCP:
		past, present = "Called", "Calling"
	}
	if running {
		return present
	}
	return past
}

func (k verbKind) noun(count int) string {
	one, many := "tool", "tools"
	switch k {
	case verbFile, verbEdit:
		one, many = "file", "files"
	case verbSkill:
		one, many = "skill", "skills"
	case verbSearch:
		one, many = "pattern", "patterns"
	case verbDir:
		one, many = "dir", "dirs"
	case verbWebFetch, verbWebSearch:
		one, many = "website", "websites"
	case verbMemory:
		one, many = "memory", "memories"
	case verbMCPSearch, verbMCP:
		one, many = "MCP tool", "MCP tools"
	case verbCommand:
		one, many = "command", "commands"
	}
	if count == 1 {
		return one
	}
	return many
}

func classifyTool(title string) verbKind {
	name := toolName(title)
	switch {
	case strings.Contains(name, "skill"):
		return verbSkill
	case name == "read" || name == "read_file" || strings.HasPrefix(name, "read_"):
		return verbFile
	case name == "search_replace" || name == "apply_patch" || name == "write" || name == "write_file" || strings.Contains(name, "edit"):
		return verbEdit
	case name == "grep" || name == "glob" || name == "rg" || name == "search":
		return verbSearch
	case name == "web_search" || name == "x_search" || strings.Contains(name, "web_search"):
		return verbWebSearch
	case name == "web_fetch" || name == "webfetch" || strings.Contains(name, "web_fetch"):
		return verbWebFetch
	case strings.Contains(name, "memory_search"):
		return verbMemory
	case name == "search_tool":
		return verbMCPSearch
	case name == "list_dir" || name == "ls" || name == "glob_file_search":
		if name == "glob_file_search" {
			return verbSearch
		}
		return verbDir
	case strings.Contains(name, "list") && !strings.Contains(name, "todo"):
		return verbDir
	case name == "bash" || name == "shell" || name == "exec":
		return verbCommand
	case strings.HasPrefix(name, "mcp_") || strings.Contains(name, "__") || name == "use_tool":
		return verbMCP
	default:
		if strings.Contains(name, "grep") || strings.Contains(name, "glob") {
			return verbSearch
		}
		if strings.Contains(name, "web") && strings.Contains(name, "fetch") {
			return verbWebFetch
		}
		if strings.Contains(name, "read") {
			return verbFile
		}
		return verbOther
	}
}

func parseToolArgs(body string) map[string]string {
	body = strings.TrimSpace(body)
	if body == "" || body[0] != '{' {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal([]byte(body), &raw) != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				out[k] = t
			}
		case float64:
			out[k] = strconv.FormatInt(int64(t), 10)
		case bool:
			out[k] = strconv.FormatBool(t)
		}
	}
	return out
}

func toolArgValue(args map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(args[k]); v != "" {
			return v
		}
	}
	return ""
}

func skillNameFromPath(path string) string {
	base := filepath.ToSlash(path)
	if !strings.HasSuffix(strings.ToLower(base), "/skill.md") && !strings.EqualFold(filepath.Base(base), "SKILL.md") {
		return ""
	}
	dir := filepath.Base(filepath.Dir(base))
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	return dir
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func toolCallHeader(title, body string) string {
	kind := classifyTool(title)
	args := parseToolArgs(body)
	switch kind {
	case verbFile:
		path := toolArgValue(args, "path", "file_path", "target_file")
		if path == "" {
			path = firstLine(body)
		}
		if skill := skillNameFromPath(path); skill != "" {
			return "Skill " + skill
		}
		if path == "" || strings.HasPrefix(path, "{") {
			return "Read"
		}
		return "Read " + path
	case verbSkill:
		name := toolArgValue(args, "skill", "name", "path")
		if name == "" {
			name = firstLine(body)
		}
		return "Skill " + name
	case verbEdit:
		path := toolArgValue(args, "path", "file_path", "target_file")
		if path == "" || strings.HasPrefix(path, "{") {
			return "Edit"
		}
		return "Edit " + path
	case verbCommand:
		cmd := toolArgValue(args, "command", "cmd")
		if cmd == "" {
			cmd = firstLine(body)
		}
		if cmd == "" || strings.HasPrefix(cmd, "{") {
			return "Run"
		}
		return "Run " + cmd
	case verbSearch:
		q := toolArgValue(args, "pattern", "query", "glob", "regex")
		if q == "" || strings.HasPrefix(q, "{") {
			return "Search"
		}
		return "Search " + q
	case verbDir:
		path := toolArgValue(args, "path", "target_directory", "dir")
		if path == "" || strings.HasPrefix(path, "{") {
			return "List"
		}
		return "List " + path
	case verbWebFetch:
		url := toolArgValue(args, "url")
		if url == "" || strings.HasPrefix(url, "{") {
			return "Fetch"
		}
		return "Fetch " + url
	case verbWebSearch:
		q := toolArgValue(args, "query", "q", "search")
		if q == "" || strings.HasPrefix(q, "{") {
			return "Search"
		}
		return "Search " + q
	case verbMemory:
		q := toolArgValue(args, "query", "q")
		if q == "" {
			return "Search"
		}
		return "Search " + q
	case verbMCP, verbMCPSearch:
		name := strings.TrimLeft(strings.TrimSpace(title), "▸ ")
		if name == "" {
			name = toolName(title)
		}
		return name
	default:
		name := strings.TrimLeft(strings.TrimSpace(title), "▸ ")
		if name == "" {
			name = "tool"
		}
		return name
	}
}

func truncateResultBody(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= toolResultFirstLines+toolResultLastLines+1 {
		return s
	}
	var b strings.Builder
	b.WriteString(strings.Join(lines[:toolResultFirstLines], "\n"))
	b.WriteString("\n…\n")
	b.WriteString(strings.Join(lines[len(lines)-toolResultLastLines:], "\n"))
	return b.String()
}

func renderAccentBlock(header, body string, failed bool) string {
	var b strings.Builder
	head := toolStyle.Render(header)
	if failed {
		head = errStyle.Render(header)
	}
	writeAccent(&b, head)
	if strings.TrimSpace(body) == "" {
		return strings.TrimRight(b.String(), "\n")
	}
	for _, line := range strings.Split(body, "\n") {
		if failed {
			writeAccent(&b, errStyle.Render(line))
		} else {
			writeAccent(&b, dimStyle.Render(line))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func userPromptPrefix(text string) string {
	trim := strings.TrimSpace(text)
	if strings.HasPrefix(trim, "!") {
		return "$ "
	}
	return promptArrow
}

func collapseUserLines(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) <= userCollapsedMaxLines {
		return text, false
	}
	return strings.Join(lines[:userCollapsedMaxLines], "\n") + " …", true
}
