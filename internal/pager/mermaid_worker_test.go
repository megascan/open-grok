package pager

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidAffordanceRowMatchesRust(t *testing.T) {
	src := "flowchart TD\n  A --> B\n"
	out := renderMermaidASCII(src, 80)
	plain := stripANSI(out)
	for _, need := range []string{
		"◇ mermaid",
		"[Open Image]",
		"[Copy Image Path]",
		"[Copy Source]",
	} {
		if !strings.Contains(plain, need) {
			t.Fatalf("missing %q:\n%s", need, plain)
		}
	}
	if strings.Contains(plain, "rendering diagram") {
		t.Fatal("render is lazy; hint must be absent until click")
	}
	busyPlain := stripANSI(renderMermaidASCIIEx(src, 80, true))
	if !strings.Contains(busyPlain, "rendering") || !strings.Contains(busyPlain, "diagram") {
		t.Fatalf("busy hint:\n%s", busyPlain)
	}
	off := mermaidFallbackCard(src, 80)
	if strings.Contains(stripANSI(off), "[Open Image]") {
		t.Fatal("off mode must not show affordance buttons")
	}
}

func TestMermaidRenderArgv(t *testing.T) {
	cfg, err := parseMermaidRenderArgs([]string{
		"--out", "/tmp/d.png",
		"--theme", "dark",
		"--quality", "open",
		"--width", "1280",
		"--deadline-ms", "2500",
		"--max-height", "999",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dark || cfg.Quality != "open" || cfg.Out != "/tmp/d.png" || cfg.Width != 1280 {
		t.Fatalf("%+v", cfg)
	}
	if _, err := parseMermaidRenderArgs([]string{"--theme", "dark"}); err == nil {
		t.Fatal("missing --out must fail")
	}
}

func TestMermaidPNGWriteAndCacheName(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "d.png")
	art := mermaidArtForPNG("flowchart TD\n  Start --> End\n", 40)
	if !strings.Contains(art, "Start") {
		t.Fatalf("art:\n%s", art)
	}
	if err := writeMermaidPNG(art, out, true, "open"); err != nil {
		t.Fatal(err)
	}
	if err := mermaidPNGDecodable(out); err != nil {
		t.Fatal(err)
	}
	name := mermaidCacheName("flowchart TD\n  A --> B\n", true, 80, true)
	if !strings.HasSuffix(name, "-o-r3.png") {
		t.Fatalf("open-tier name %q", name)
	}
	term := mermaidCacheName("flowchart TD\n  A --> B\n", false, 80, false)
	if !strings.Contains(term, "-t-r3.png") {
		t.Fatalf("terminal-tier name %q", term)
	}
	if hashMermaidSource("a") == hashMermaidSource("b") {
		t.Fatal("hashes must differ")
	}
}

func TestMermaidCopySourceSkipsRender(t *testing.T) {
	m := testModel(t)
	src := "flowchart TD\n  A --> B\n"
	cmd := m.clickMermaid(src, mermaidClickCopySource)
	if cmd == nil {
		t.Fatal("copy source should yield a cmd")
	}
	if m.mermaidPendingFor(src) {
		t.Fatal("copy source must not start a render")
	}
}

func TestMarkdownMermaidFenceShowsAffordances(t *testing.T) {
	md := renderMarkdownEx("```mermaid\nflowchart TD\n  A --> B\n```", 80, true)
	plain := stripANSI(md)
	if !strings.Contains(plain, "[Copy Source]") || !strings.Contains(plain, "◇ mermaid") {
		t.Fatalf("markdown fence affordances:\n%s", plain)
	}
}
