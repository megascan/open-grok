package agent_test

import (
	"testing"

	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

func TestCodexCodeModeOnlyWinsOnResponses(t *testing.T) {
	m, ok := catalog.Lookup("gpt-5.6-sol")
	if !ok {
		t.Fatal("sol")
	}
	if agent.EffectiveToolMode(m) != types.ToolModeCodeModeOnly {
		t.Fatalf("got %s", agent.EffectiveToolMode(m))
	}
	pack := agent.PackFor(m, nil)
	names := map[string]bool{}
	for _, p := range pack {
		names[p.Name()] = true
	}
	if names["bash"] {
		t.Fatal("Code Mode Only must not expose bash top-level")
	}
	if !names["ask_user_question"] || !names["task"] || !names["update_goal"] {
		t.Fatal("direct-only human/multi-agent tools stay top-level")
	}
}

func TestNonResponsesFailsClosedToDirect(t *testing.T) {
	m, _ := catalog.Lookup("kimi-k3")
	if agent.EffectiveToolMode(m) != types.ToolModeDirect {
		t.Fatal("chat completions cannot enable Code Mode")
	}
}
