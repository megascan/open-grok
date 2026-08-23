package pager

import (
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
	if got := toolCallHeader("grep", `{"pattern":"TODO"}`); got != "Search TODO" {
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
