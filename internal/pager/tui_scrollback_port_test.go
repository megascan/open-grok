package pager

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestUserBlockUsesPromptArrowNotYOU(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{{kind: blockUser, body: "hello world"}}
	view := stripANSI(m.renderTranscript())
	if strings.Contains(view, "YOU") {
		t.Fatalf("YOU label leaked:\n%s", view)
	}
	if !strings.Contains(view, "hello world") {
		t.Fatalf("body:\n%s", view)
	}
	if !strings.Contains(view, strings.TrimSpace(promptArrow)) {
		t.Fatalf("missing prompt arrow:\n%s", view)
	}
}

func TestAgentBlockHasNoEntropyLabel(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{{kind: blockAgent, body: "the answer"}}
	view := stripANSI(m.renderTranscript())
	if strings.Contains(view, "ENTROPY") {
		t.Fatalf("ENTROPY label leaked:\n%s", view)
	}
	if !strings.Contains(view, "the answer") {
		t.Fatalf("%s", view)
	}
}

func TestThinkingBlockThoughtHeader(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{{kind: blockThinking, body: "maybe this"}}
	view := stripANSI(m.renderTranscript())
	if strings.Contains(view, "thinking\n") {
		t.Fatalf("old header:\n%s", view)
	}
	if !strings.Contains(view, "Thought") {
		t.Fatalf("missing Thought:\n%s", view)
	}
	m.busy = true
	view = stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Thinking…") {
		t.Fatalf("running:\n%s", view)
	}
}

func TestToolCardReadAndRunHeaders(t *testing.T) {
	m := testModel(t)
	m.groupTools = true
	m.blocks = []block{
		{kind: blockTool, title: "read_file", body: `{"path":"src/main.go"}`},
		{kind: blockToolResult, title: "read_file", body: "package main\n"},
		{kind: blockTool, title: "bash", body: `{"command":"cargo test"}`},
		{kind: blockToolResult, title: "bash", body: "ok\n"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Read src/main.go") {
		t.Fatalf("read:\n%s", view)
	}
	if !strings.Contains(view, "Run cargo test") {
		t.Fatalf("run:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("accent")
	}
	if strings.Contains(view, "YOU") || strings.Contains(view, "ENTROPY") {
		t.Fatalf("role labels:\n%s", view)
	}
}

func TestToolCardsDoNotEagerlyGroupBash(t *testing.T) {
	m := testModel(t)
	m.groupTools = true
	m.blocks = []block{
		{kind: blockTool, title: "bash", body: `{"command":"ls"}`},
		{kind: blockToolResult, title: "bash", body: "a"},
		{kind: blockTool, title: "bash", body: `{"command":"pwd"}`},
		{kind: blockToolResult, title: "bash", body: "b"},
	}
	view := stripANSI(m.renderTranscript())
	if strings.Contains(view, "Ran 2 commands") {
		t.Fatalf("bash must not fold:\n%s", view)
	}
	if !strings.Contains(view, "Run ls") || !strings.Contains(view, "Run pwd") {
		t.Fatalf("standalone:\n%s", view)
	}
}

func TestReadToolsGroupAndExpand(t *testing.T) {
	m := testModel(t)
	m.groupTools = true
	m.blocks = []block{
		{kind: blockTool, title: "read_file", body: `{"path":"a.go"}`},
		{kind: blockToolResult, title: "read_file", body: "A"},
		{kind: blockTool, title: "read_file", body: `{"path":"b.go"}`},
		{kind: blockToolResult, title: "read_file", body: "B"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Read 2 files") {
		t.Fatalf("group:\n%s", view)
	}
	if strings.Contains(view, "Read a.go") {
		t.Fatalf("collapsed should hide members:\n%s", view)
	}
	m.toggleFold(0)
	view = stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Read a.go") || !strings.Contains(view, "Read b.go") {
		t.Fatalf("expanded:\n%s", view)
	}
}

func TestToolCallHeaderSkillAndSearch(t *testing.T) {
	if got := toolCallHeader("read_file", `{"path":"/ws/.agents/skills/foo/SKILL.md"}`); got != "Skill foo" {
		t.Fatalf("skill %q", got)
	}
	if got := toolCallHeader("grep", `{"pattern":"TODO"}`); got != `Search "TODO"` {
		t.Fatalf("search %q", got)
	}
	if got := toolCallHeader("list_dir", `{"path":"src"}`); got != "List src" {
		t.Fatalf("list %q", got)
	}
	if got := toolCallHeader("web_fetch", `{"url":"https://x.ai"}`); got != "Fetch https://x.ai" {
		t.Fatalf("fetch %q", got)
	}
	if got := toolCallHeader("search_replace", `{"path":"main.go"}`); got != "Edit main.go" {
		t.Fatalf("edit %q", got)
	}
}

func TestLongUserPromptAutoFolds(t *testing.T) {
	m := testModel(t)
	body := "one\ntwo\nthree\nfour\nfive"
	m.appendBlock(block{kind: blockUser, body: body})
	if !m.blocks[0].folded {
		t.Fatal("long prompt should fold")
	}
	view := stripANSI(m.renderUserBlock(m.blocks[0], 72))
	if strings.Contains(view, "five") {
		t.Fatalf("collapsed still shows tail:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("ellipsis:\n%s", view)
	}
}

func TestEditCardShowsDiffstatAndHunks(t *testing.T) {
	m := testModel(t)
	args := `{"path":"main.go","old_string":"foo","new_string":"bar"}`
	m.blocks = []block{
		{kind: blockTool, title: "search_replace", body: args, folded: true},
		{kind: blockToolResult, title: "search_replace", body: "updated main.go (1 replacement(s))"},
	}
	collapsed := stripANSI(m.renderTranscript())
	if !strings.Contains(collapsed, "Edit main.go") {
		t.Fatalf("header:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "+1") || !strings.Contains(collapsed, "-1") {
		t.Fatalf("diffstat:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "foo") || strings.Contains(collapsed, "bar") {
		t.Fatalf("collapsed must hide hunks:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "updated main.go") {
		t.Fatalf("raw result leaked:\n%s", collapsed)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderTranscript())
	if !strings.Contains(expanded, "foo") || !strings.Contains(expanded, "bar") {
		t.Fatalf("hunks:\n%s", expanded)
	}
	if strings.Contains(expanded, "+1/-1") || strings.Contains(expanded, " +1/") {
		t.Fatalf("expanded header should drop diffstat:\n%s", expanded)
	}
}

func TestWriteFileCardUsesCreatingPrefix(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "write_file", body: `{"path":"hello.go","contents":"package main\n"}`, folded: true},
		{kind: blockToolResult, title: "write_file", body: "wrote hello.go"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Creating hello.go") {
		t.Fatalf("creating:\n%s", view)
	}
	if !strings.Contains(view, "+1") {
		t.Fatalf("insert count:\n%s", view)
	}
}

func TestApplyPatchCardRendersHunks(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: src/a.go\n@@\n-old\n+new\n*** End Patch"
	quoted, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "apply_patch", body: `{"patch":` + string(quoted) + `}`},
		{kind: blockToolResult, title: "apply_patch", body: "update src/a.go"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Edit src/a.go") {
		t.Fatalf("header:\n%s", view)
	}
	if !strings.Contains(view, "old") || !strings.Contains(view, "new") {
		t.Fatalf("hunks default expanded:\n%s", view)
	}
}

func TestReadCardLineRangeAndTruncation(t *testing.T) {
	m := testModel(t)
	var result strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&result, "%6d|line %d\n", i, i)
	}
	m.blocks = []block{
		{kind: blockTool, title: "read_file", body: `{"path":"f.rs","offset":1,"limit":20}`, folded: true},
		{kind: blockToolResult, title: "read_file", body: result.String()},
	}
	collapsed := stripANSI(m.renderBlockAt(0, 72))
	if !strings.Contains(collapsed, "Read f.rs (1-20)") {
		t.Fatalf("range:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "line 1") {
		t.Fatalf("collapsed should hide content:\n%s", collapsed)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderBlockAt(0, 72))
	if !strings.Contains(expanded, "line 1") || !strings.Contains(expanded, "line 20") {
		t.Fatalf("truncated ends:\n%s", expanded)
	}
	if strings.Contains(expanded, "line 10") {
		t.Fatalf("middle should be elided:\n%s", expanded)
	}
	if !strings.Contains(expanded, "…") {
		t.Fatalf("ellipsis:\n%s", expanded)
	}
}

func TestSearchCardQuotesPatternAndCounts(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "grep", body: `{"pattern":"TODO"}`, folded: true},
		{kind: blockToolResult, title: "grep", body: "a.go:1:TODO here\nb.go:2:TODO there"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, `Search "TODO"`) {
		t.Fatalf("quoted:\n%s", view)
	}
	if !strings.Contains(view, "(2 matches in 2 files)") {
		t.Fatalf("summary:\n%s", view)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderTranscript())
	if !strings.Contains(expanded, "a.go") || !strings.Contains(expanded, "TODO here") {
		t.Fatalf("hits:\n%s", expanded)
	}
}

func TestListCardEntryCount(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "list_dir", body: `{"path":"src"}`, folded: true},
		{kind: blockToolResult, title: "list_dir", body: "a.rs\nb.rs\nsub/\n"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "List src (3 entries)") {
		t.Fatalf("count:\n%s", view)
	}
}

func TestRunCardTruncatesWithHiddenCount(t *testing.T) {
	m := testModel(t)
	var out strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	m.blocks = []block{
		{kind: blockTool, title: "bash", body: `{"command":"seq 10"}`, folded: true},
		{kind: blockToolResult, title: "bash", body: out.String()},
	}
	collapsed := stripANSI(m.renderBlockAt(0, 72))
	if strings.Contains(collapsed, "line 1") {
		t.Fatalf("collapsed stdout:\n%s", collapsed)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderBlockAt(0, 72))
	if !strings.Contains(expanded, "line 1") || !strings.Contains(expanded, "line 10") {
		t.Fatalf("ends:\n%s", expanded)
	}
	if !strings.Contains(expanded, "… +5 lines") {
		t.Fatalf("hidden count:\n%s", expanded)
	}
	if strings.Contains(expanded, "line 5") {
		t.Fatalf("middle leaked:\n%s", expanded)
	}
}

func TestAppendBlockFoldsReadsNotEditsByDefault(t *testing.T) {
	m := testModel(t)
	m.appendBlock(block{kind: blockTool, title: "read_file", body: `{"path":"a.go"}`})
	m.appendBlock(block{kind: blockTool, title: "search_replace", body: `{"path":"a.go","old_string":"a","new_string":"b"}`})
	if !m.blocks[0].folded {
		t.Fatal("reads should start collapsed")
	}
	if m.blocks[1].folded {
		t.Fatal("edits should start expanded when collapsed_edit_blocks is off")
	}
}

func TestLcsDiffKeepsEqualLines(t *testing.T) {
	hunk := hunkFromReplace("a\nb\nc", "a\nx\nc")
	if count := len(hunk); count != 4 {
		t.Fatalf("len %d %#v", count, hunk)
	}
	ins, del := countInsDel(hunk)
	if ins != 1 || del != 1 {
		t.Fatalf("ins=%d del=%d", ins, del)
	}
	if hunk[0].Tag != tagEqual || hunk[0].Text != "a" {
		t.Fatalf("first %#v", hunk[0])
	}
	if hunk[3].Tag != tagEqual || hunk[3].Text != "c" {
		t.Fatalf("last %#v", hunk[3])
	}
}

func TestWebSearchCardHeaderAndSources(t *testing.T) {
	if got := toolCallHeader("web_search", `{"query":"rust tui"}`); got != "Web Search rust tui" {
		t.Fatalf("header %q", got)
	}
	if got := toolCallHeader("x_search", `{"query":"grok"}`); got != "X Search grok" {
		t.Fatalf("x %q", got)
	}
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "web_search", body: `{"query":"rust tui"}`, folded: true},
		{kind: blockToolResult, title: "web_search", body: "see https://example.com/a and https://docs.rs/ratatui"},
	}
	collapsed := stripANSI(m.renderTranscript())
	if !strings.Contains(collapsed, "Web Search rust tui (2 sites)") {
		t.Fatalf("sites:\n%s", collapsed)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderTranscript())
	if strings.Contains(expanded, "(2 sites)") {
		t.Fatalf("expanded drops site suffix:\n%s", expanded)
	}
	if !strings.Contains(expanded, "Sources:") || !strings.Contains(expanded, "example.com") {
		t.Fatalf("sources:\n%s", expanded)
	}
}

func TestMemorySearchCardHeaderAndHits(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "memory_search", body: `{"query":"pager"}`, folded: true},
		{kind: blockToolResult, title: "memory_search", body: "## notes.md\nalpha line\n## other.md\nbravo line\n"},
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Memory Search pager (2 results)") {
		t.Fatalf("header:\n%s", view)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderTranscript())
	if !strings.Contains(expanded, "1. notes.md") || !strings.Contains(expanded, "alpha line") {
		t.Fatalf("hits:\n%s", expanded)
	}
}

func TestMCPToolCardTitleizesQualifiedName(t *testing.T) {
	if got := toolCallHeader("linear__list_issues", `{"id":"ABC"}`); got != "Linear List Issues" {
		t.Fatalf("mcp %q", got)
	}
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "linear__list_issues", body: `{"id":"ABC"}`},
		{kind: blockToolResult, title: "linear__list_issues", body: "ok"},
	}
	expanded := stripANSI(m.renderTranscript())
	if !strings.Contains(expanded, "Linear List Issues") {
		t.Fatalf("header:\n%s", expanded)
	}
	if !strings.Contains(expanded, "id:") || !strings.Contains(expanded, "ABC") {
		t.Fatalf("args:\n%s", expanded)
	}
}

func TestSearchToolsHeader(t *testing.T) {
	if got := toolCallHeaderFull("search_tool", `{"query":"linear"}`, "a\nb\n", true); !strings.Contains(got, "Search Tools linear") {
		t.Fatalf("header %q", got)
	}
	if got := toolCallHeaderFull("search_tool", `{"query":"linear"}`, "a\nb\n", true); !strings.Contains(got, "(2 results)") {
		t.Fatalf("count %q", got)
	}
}
