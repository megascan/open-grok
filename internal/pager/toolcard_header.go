package pager

import (
	"path/filepath"
	"strconv"
	"strings"
)

func toolCallHeader(title, body string) string {
	return toolCallHeaderFull(title, body, "", true)
}

func toolCallHeaderFull(title, body, result string, collapsed bool) string {
	kind := classifyTool(title)
	args := parseToolArgs(body)
	switch kind {
	case verbFile:
		return readHeader(args, body, result)
	case verbSkill:
		name := toolArgValue(args, "skill", "name", "path")
		if name == "" {
			name = firstLine(body)
		}
		return "Skill " + name
	case verbEdit:
		return editHeaderText(title, args, body, collapsed)
	case verbCommand:
		cmd := commandFromArgs(args, body)
		if cmd == "" {
			return "Run"
		}
		return "Run " + cmd
	case verbSearch:
		return searchHeader(args, body, result, false)
	case verbDir:
		return listHeader(args, body, result)
	case verbWebFetch:
		url := toolArgValue(args, "url")
		if url == "" || strings.HasPrefix(url, "{") {
			return "Fetch"
		}
		return "Fetch " + url
	case verbWebSearch:
		return webSearchHeader(title, args, body, result, collapsed)
	case verbMemory:
		return memorySearchHeader(args, body, result)
	case verbMCP:
		return mcpToolHeader(title, args)
	case verbMCPSearch:
		return searchToolsHeader(args, body, result)
	default:
		return otherToolHeader(title, args, body)
	}
}

func commandFromArgs(args map[string]string, body string) string {
	cmd := toolArgValue(args, "command", "cmd")
	if cmd == "" {
		cmd = firstLine(body)
	}
	if cmd == "" || strings.HasPrefix(cmd, "{") {
		return ""
	}
	return cmd
}

func readHeader(args map[string]string, body, result string) string {
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
	header := "Read " + path
	if rangeSuffix := readRangeSuffix(args, result); rangeSuffix != "" {
		header += rangeSuffix
	}
	if extra := readExtraSuffix(path, result); extra != "" {
		header += extra
	}
	return header
}

func readRangeSuffix(args map[string]string, result string) string {
	start, _ := strconv.Atoi(toolArgValue(args, "offset", "start_line", "start"))
	limit, _ := strconv.Atoi(toolArgValue(args, "limit", "end_line"))
	endArg, _ := strconv.Atoi(toolArgValue(args, "end_line", "end"))
	if start <= 0 && endArg <= 0 && limit <= 0 {
		return ""
	}
	if start <= 0 {
		start = 1
	}
	end := endArg
	if end <= 0 && limit > 0 {
		end = start + limit - 1
	}
	if end <= 0 {
		if n := countResultLines(result); n > 0 {
			end = start + n - 1
		} else {
			end = start
		}
	}
	total := parseReadTotal(result, end)
	if total > end-start+1 {
		return " (" + strconv.Itoa(start) + "-" + strconv.Itoa(end) + " of " + strconv.Itoa(total) + ")"
	}
	return " (" + strconv.Itoa(start) + "-" + strconv.Itoa(end) + ")"
}

func readExtraSuffix(path, result string) string {
	if strings.TrimSpace(result) == "" {
		return ""
	}
	if isEmptyReadResult(result) {
		return " (empty)"
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		return " (image)"
	case strings.HasSuffix(lower, ".pdf"):
		return " (pdf)"
	}
	return ""
}

func isEmptyReadResult(result string) bool {
	s := strings.TrimSpace(result)
	return s == "" || s == "(empty)" || s == "empty"
}

func parseReadTotal(result string, end int) int {
	last := 0
	for _, line := range strings.Split(result, "\n") {
		if n, ok := parseReadGutter(line); ok && n > last {
			last = n
		}
	}
	if last > end {
		return last
	}
	return 0
}

func parseReadGutter(line string) (int, bool) {
	line = strings.TrimRight(line, "\r")
	bar := strings.IndexByte(line, '|')
	if bar <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[:bar]))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func countResultLines(result string) int {
	s := strings.TrimRight(result, "\n")
	if s == "" {
		return 0
	}
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func listHeader(args map[string]string, body, result string) string {
	path := toolArgValue(args, "path", "target_directory", "dir")
	if path == "" || strings.HasPrefix(path, "{") {
		if path == "" {
			path = firstLine(body)
		}
		if path == "" || strings.HasPrefix(path, "{") {
			path = ""
		}
	}
	header := "List"
	if path != "" {
		header += " " + path
	}
	if n := countResultLines(result); n > 0 {
		if n == 1 {
			header += " (1 entry)"
		} else {
			header += " (" + strconv.Itoa(n) + " entries)"
		}
	}
	return header
}

func searchHeader(args map[string]string, body, result string, web bool) string {
	pattern := toolArgValue(args, "pattern", "query", "q", "search", "regex")
	glob := toolArgValue(args, "glob")
	path := toolArgValue(args, "path", "target_directory")
	if pattern == "" {
		pattern = firstLine(body)
	}
	if pattern == "" || strings.HasPrefix(pattern, "{") {
		if glob == "" {
			return "Search"
		}
		pattern = ""
	}
	var b strings.Builder
	b.WriteString("Search ")
	trivial := pattern == "" || pattern == "."
	if trivial && glob != "" {
		b.WriteString(glob)
	} else {
		if pattern == "" {
			return "Search"
		}
		b.WriteString(strconv.Quote(pattern))
		if glob != "" {
			b.WriteString(" in ")
			b.WriteString(glob)
		}
	}
	if path != "" && !web {
		b.WriteString(" in ")
		b.WriteString(path)
	}
	if result != "" {
		b.WriteByte(' ')
		b.WriteString(searchSummary(result, glob != "" && trivial))
	}
	return b.String()
}

func searchSummary(result string, filesOnly bool) string {
	s := strings.TrimSpace(result)
	if s == "" || s == "no matches" || s == "no files matched" {
		if filesOnly {
			return "(no files)"
		}
		return "(no matches)"
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	files := map[string]struct{}{}
	matches := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matches++
		path := line
		if i := strings.IndexByte(line, ':'); i > 0 {
			path = line[:i]
		}
		files[path] = struct{}{}
	}
	if matches == 0 {
		if filesOnly {
			return "(no files)"
		}
		return "(no matches)"
	}
	if filesOnly || !strings.Contains(lines[0], ":") {
		if matches == 1 {
			return "(1 file)"
		}
		return "(" + strconv.Itoa(matches) + " files)"
	}
	if len(files) > 1 {
		return "(" + strconv.Itoa(matches) + " matches in " + strconv.Itoa(len(files)) + " files)"
	}
	if matches == 1 {
		return "(1 match)"
	}
	return "(" + strconv.Itoa(matches) + " matches)"
}

func editHeaderText(title string, args map[string]string, body string, collapsed bool) string {
	card := parseEditCard(title, args, body)
	header := card.Prefix + card.Path
	if strings.TrimSpace(card.Path) == "" {
		header = strings.TrimSpace(card.Prefix)
		if header == "" {
			header = "Edit"
		}
	}
	if !collapsed || card.Untrusted {
		return strings.TrimSpace(header)
	}
	ins, del := countInsDelHunks(card.Hunks)
	if ins > 0 || del > 0 {
		return strings.TrimSpace(header) + " +" + strconv.Itoa(ins) + "/-" + strconv.Itoa(del)
	}
	if card.EditCount > 1 {
		return strings.TrimSpace(header) + " (" + strconv.Itoa(card.EditCount) + " edits)"
	}
	return strings.TrimSpace(header)
}

type editCard struct {
	Prefix    string
	Path      string
	Hunks     [][]diffLine
	Untrusted bool
	EditCount int
	Creating  bool
}

func parseEditCard(title string, args map[string]string, body string) editCard {
	name := toolName(title)
	path := toolArgValue(args, "path", "file_path", "target_file")
	card := editCard{Prefix: "Edit ", Path: path}
	if wf := workflowScriptName(path); wf != "" {
		card.Prefix = "Editing workflow "
		card.Path = wf
	}
	switch {
	case name == "write" || name == "write_file":
		card.Prefix = "Creating "
		if wf := workflowScriptName(path); wf != "" {
			card.Prefix = "Creating workflow "
			card.Path = wf
		}
		if c := toolArgValue(args, "contents", "content", "text"); c != "" {
			card.Hunks = [][]diffLine{hunkFromWrite(c)}
		}
	case name == "apply_patch":
		patch := toolArgValue(args, "patch", "input")
		if patch == "" {
			patch = body
		}
		disp := parsePatchDisplay(patch)
		if disp.Path != "" {
			card.Path = disp.Path
			if wf := workflowScriptName(disp.Path); wf != "" {
				card.Path = wf
				if disp.Creating {
					card.Prefix = "Creating workflow "
				} else {
					card.Prefix = "Editing workflow "
				}
			} else {
				card.Prefix = disp.Prefix
			}
		}
		card.Hunks = disp.Hunks
		card.Untrusted = disp.Untrusted
		card.Creating = disp.Creating
		card.EditCount = len(disp.Hunks)
	default:
		oldS := toolArgValue(args, "old_string", "old")
		newS := toolArgValue(args, "new_string", "new")
		if oldS != "" || newS != "" {
			card.Hunks = [][]diffLine{hunkFromReplace(oldS, newS)}
		}
	}
	if card.Path == "" || strings.HasPrefix(card.Path, "{") {
		card.Path = ""
	}
	if card.EditCount == 0 {
		card.EditCount = len(card.Hunks)
	}
	return card
}

func workflowScriptName(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasSuffix(strings.ToLower(p), ".rhai") {
		return ""
	}
	if !strings.Contains(p, "/workflows/") && !strings.HasPrefix(p, "workflows/") {
		return ""
	}
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
