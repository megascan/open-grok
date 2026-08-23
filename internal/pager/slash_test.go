package pager

import (
	"strings"
	"testing"
)

func TestParseGoalSlash(t *testing.T) {
	if parseGoalSlash("").Verb != goalVerbStatus {
		t.Fatal("empty is status")
	}
	if parseGoalSlash("pause").Verb != goalVerbPause {
		t.Fatal("pause")
	}
	got := parseGoalSlash("Migrate auth --budget 8000")
	if got.Verb != goalVerbStart || got.Objective != "Migrate auth" || got.Budget == nil || *got.Budget != 8000 {
		t.Fatalf("%+v", got)
	}
}

func TestMatchingSlashIncludesGoal(t *testing.T) {
	found := false
	for _, c := range matchingSlash("/go") {
		if c.Name == "goal" {
			found = true
		}
	}
	if !found {
		t.Fatal("/go should suggest /goal")
	}
}

func TestRenderMarkdownHeadersAndBullets(t *testing.T) {
	out := renderMarkdown("# Title\n- item\nplain `code`", 80)
	if !strings.Contains(stripANSI(out), "Title") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(stripANSI(out), "•") {
		t.Fatalf("bullets: %q", out)
	}
}
