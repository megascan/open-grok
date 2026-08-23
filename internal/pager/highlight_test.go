package pager

import (
	"strings"
	"testing"
)

func TestHighlightGoKeywords(t *testing.T) {
	lines := highlightSourceLines("main.go", []string{
		"package main",
		"func Hello() string { return \"hi\" }",
	})
	if len(lines) != 2 {
		t.Fatalf("lines %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if joined == "package main\nfunc Hello() string { return \"hi\" }" {
		t.Fatalf("expected chroma paint, got plain %q", joined)
	}
	if !strings.Contains(stripANSI(joined), "package main") || !strings.Contains(stripANSI(joined), "func Hello") {
		t.Fatalf("lost source: %q", stripANSI(joined))
	}
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("missing ANSI: %q", joined)
	}
}

func TestHighlightUnknownPathStaysPlain(t *testing.T) {
	src := []string{"not a known language file"}
	got := highlightSourceLines("notes.unknownext", src)
	if len(got) != 1 || got[0] != src[0] {
		t.Fatalf("%q", got)
	}
}

func TestReadCardHighlightsRustSource(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "read_file", body: `{"path":"src/lib.rs"}`, folded: false},
		{kind: blockToolResult, title: "read_file", body: "     1|fn main() {\n     2|    println!(\"hi\");\n     3|}\n"},
	}
	raw := m.renderBlockAt(0, 72)
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected syntax color:\n%s", raw)
	}
	plain := stripANSI(raw)
	if !strings.Contains(plain, "fn main()") || !strings.Contains(plain, "println!") {
		t.Fatalf("source:\n%s", plain)
	}
}

func TestEditCardHighlightsHunks(t *testing.T) {
	m := testModel(t)
	m.blocks = []block{
		{kind: blockTool, title: "search_replace", body: `{"path":"app.go","old_string":"func a() {}","new_string":"func a() { return }"}`, folded: false},
		{kind: blockToolResult, title: "search_replace", body: "ok"},
	}
	raw := m.renderBlockAt(0, 72)
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected paint:\n%s", raw)
	}
	if !strings.Contains(stripANSI(raw), "func a()") {
		t.Fatalf("%s", stripANSI(raw))
	}
}

func TestMarkdownFenceHighlightsLang(t *testing.T) {
	got := renderMarkdownEx("```go\nfunc main() {}\n```", 80, false)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("fence should highlight:\n%s", got)
	}
	if !strings.Contains(stripANSI(got), "func main()") {
		t.Fatalf("%s", stripANSI(got))
	}
}
