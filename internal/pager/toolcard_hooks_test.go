package pager

import (
	"strings"
	"testing"
	"time"

	"github.com/megascan/open-grok/internal/hooks"
)

func TestToolCardHookSuffixAndDetail(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.blocks = []block{
		{
			kind:  blockTool,
			title: "bash",
			body:  `{"command":"ls"}`,
			hooks: toolHookData{Pre: []hookRun{{
				Name:    "global/notify",
				Phase:   "pre_tool_use",
				Status:  hooks.StatusSuccess,
				Elapsed: 12 * time.Millisecond,
			}}},
			folded: true,
		},
		{kind: blockToolResult, title: "bash", body: "ok"},
	}
	collapsed := stripANSI(m.renderTranscript())
	if !strings.Contains(collapsed, "[hooks:") {
		t.Fatalf("collapsed suffix:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "global/notify") {
		t.Fatalf("collapsed hides detail:\n%s", collapsed)
	}
	m.blocks[0].folded = false
	expanded := stripANSI(m.renderTranscript())
	if !strings.Contains(expanded, "pre_tool_use") || !strings.Contains(expanded, "global/notify") {
		t.Fatalf("expanded:\n%s", expanded)
	}
	if !strings.Contains(expanded, hookCheck) {
		t.Fatalf("check mark:\n%s", expanded)
	}
}

func TestHookDataFromSplitsPhases(t *testing.T) {
	d := hookDataFrom([]hooks.Run{
		{Name: "pre", Phase: "pre_tool_use", Status: hooks.StatusSuccess},
		{Name: "post", Phase: "post_tool_use", Status: hooks.StatusFailed, Error: "boom"},
	})
	if len(d.Pre) != 1 || len(d.Post) != 1 {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(stripANSI(d.inlineSuffix()), "1/1") {
		t.Fatalf("suffix %q", stripANSI(d.inlineSuffix()))
	}
}
