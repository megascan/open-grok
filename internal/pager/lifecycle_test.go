package pager

import (
	"strings"
	"testing"
	"time"

	"github.com/megascan/open-grok/internal/agent"
)

func TestSubagentBlockMatchesRustCopy(t *testing.T) {
	m := testModel(t)
	m.busy = true
	running := stripANSI(m.renderSubagentBlock(block{kind: blockSubagent, subagent: &subagentCard{
		Desc: "explore the pager", SubType: "explore", Status: "running",
	}}, 80))
	if !strings.Contains(running, "Subagent [explore]") {
		t.Fatalf("identity: %q", running)
	}
	if !strings.Contains(running, "\u203a") {
		t.Fatalf("angle: %q", running)
	}
	if !strings.Contains(running, "running: ") {
		t.Fatalf("verb: %q", running)
	}
	if !strings.Contains(running, "\u201cexplore the pager\u201d") {
		t.Fatalf("quotes: %q", running)
	}

	started := stripANSI(m.renderSubagentBlock(block{kind: blockSubagent, subagent: &subagentCard{
		Desc: "watch tests", SubType: "general-purpose", Background: true, Status: "running",
	}}, 80))
	if !strings.Contains(started, "started: ") {
		t.Fatalf("bg started: %q", started)
	}

	done := stripANSI(m.renderSubagentBlock(block{kind: blockSubagent, subagent: &subagentCard{
		Desc: "explore the pager", Status: "completed", Elapsed: 43 * time.Second,
	}}, 80))
	if !strings.Contains(done, "completed in 43s: ") {
		t.Fatalf("completed: %q", done)
	}

	failed := stripANSI(m.renderSubagentBlock(block{kind: blockSubagent, subagent: &subagentCard{
		Desc: "explore the pager", Status: "failed", Elapsed: 2 * time.Second, Err: "boom",
	}}, 80))
	if !strings.Contains(failed, "failed in 2.0s (boom): ") {
		t.Fatalf("failed: %q", failed)
	}
}

func TestSwarmBlockMatchesRustCopy(t *testing.T) {
	m := testModel(t)
	got := stripANSI(m.renderSwarmBlock(block{kind: blockSwarm, swarm: &swarmCard{
		Desc: "split the port", Expected: 2,
		Members: []swarmMember{
			{Index: 0, Item: "scout files", Status: "running", Turns: 2, Tools: 4},
			{Index: 1, Item: "implement", Status: "queued"},
		},
	}}, 100))
	if !strings.Contains(got, "Swarm") || !strings.Contains(got, "\u00d72") {
		t.Fatalf("header: %q", got)
	}
	if !strings.Contains(got, "1 done") && !strings.Contains(got, "0 done · 0 failed") {
		t.Fatalf("counts: %q", got)
	}
	if !strings.Contains(got, "\u251c\u2500") || !strings.Contains(got, "\u2514\u2500") {
		t.Fatalf("tree: %q", got)
	}
	if !strings.Contains(got, " 01 ") || !strings.Contains(got, "running") {
		t.Fatalf("member: %q", got)
	}
	if !strings.Contains(got, "2 turns") || !strings.Contains(got, "4 tools") {
		t.Fatalf("detail: %q", got)
	}
}

func TestBgTaskBlockMatchesRustCopy(t *testing.T) {
	m := testModel(t)
	m.busy = true
	started := stripANSI(m.renderBgTaskBlock(block{kind: blockBgTask, bgTask: &bgTaskCard{
		Command: "cargo test", Desc: "run tests", Status: "started",
	}}, 80))
	if !strings.Contains(started, "Task ") || !strings.Contains(started, "started: ") || !strings.Contains(started, "run tests") {
		t.Fatalf("started: %q", started)
	}
	done := stripANSI(m.renderBgTaskBlock(block{kind: blockBgTask, bgTask: &bgTaskCard{
		Desc: "run tests", Status: "completed", Elapsed: time.Second,
	}}, 80))
	if !strings.Contains(done, "completed in 1.0s: ") {
		t.Fatalf("completed: %q", done)
	}
	killed := stripANSI(m.renderBgTaskBlock(block{kind: blockBgTask, bgTask: &bgTaskCard{
		Desc: "run tests", Status: "killed", Elapsed: 3 * time.Second,
	}}, 80))
	if !strings.Contains(killed, "killed in 3.0s: ") {
		t.Fatalf("killed: %q", killed)
	}
	failed := stripANSI(m.renderBgTaskBlock(block{kind: blockBgTask, bgTask: &bgTaskCard{
		Desc: "run tests", Status: "failed", Elapsed: 4 * time.Second, HasExit: true, ExitCode: 2,
	}}, 80))
	if !strings.Contains(failed, "failed in 4.0s: ") || !strings.Contains(failed, "(exit 2)") {
		t.Fatalf("failed: %q", failed)
	}
}

func TestWorkflowCardMatchesRustCopy(t *testing.T) {
	m := testModel(t)
	got := stripANSI(m.renderWorkflowCard(block{kind: blockWorkflow, workflow: &workflowRun{
		Name: "Ultracode", Objective: "port the pager", Status: "active",
		Phases: []workflowPhase{
			{Title: "Scout", Status: "done"},
			{Title: "Plan", Status: "running"},
		},
		Agents: []workflowAgent{{State: "running"}},
	}}, 100))
	if !strings.Contains(got, "Workflow ") || !strings.Contains(got, "Ultracode: ") {
		t.Fatalf("head: %q", got)
	}
	if !strings.Contains(got, "port the pager") {
		t.Fatalf("objective: %q", got)
	}
	if !strings.Contains(got, "Scout ✓") || !strings.Contains(got, "Plan ●") {
		t.Fatalf("trail: %q", got)
	}
	if !strings.Contains(got, "(1 agents)") {
		t.Fatalf("agents: %q", got)
	}
	done := stripANSI(m.renderWorkflowCard(block{kind: blockWorkflow, workflow: &workflowRun{
		Name: "Ultracode", Objective: "port the pager", Status: "complete", Elapsed: 12 * time.Second,
	}}, 80))
	if !strings.Contains(done, "Ultracode done in 12s: ") {
		t.Fatalf("done: %q", done)
	}
}

func TestApplyStreamEmitsLifecycleCards(t *testing.T) {
	m := testModel(t)
	m.applyStream(agent.Event{
		Kind: "tool", Tool: "task",
		Text: `{"description":"explore foo","subagent_type":"explore"}`,
	})
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != blockSubagent {
		t.Fatalf("task card: %+v", m.blocks)
	}
	card := m.blocks[len(m.blocks)-1].subagent
	if card == nil || card.Desc != "explore foo" || card.SubType != "explore" || card.Status != "running" {
		t.Fatalf("parsed %+v", card)
	}
	m.turnStarted = time.Now().Add(-5 * time.Second)
	m.applyStream(agent.Event{Kind: "tool_result", Tool: "task", Text: "sess-abc done"})
	if card.Status != "completed" || card.Elapsed < time.Second {
		t.Fatalf("finish %+v", card)
	}

	m.applyStream(agent.Event{
		Kind: "tool", Tool: "scheduler_create",
		Text: `{"prompt":"check ci","task_id":"loop-1"}`,
	})
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockBgTask || last.bgTask == nil || last.bgTask.Desc != "check ci" {
		t.Fatalf("bg %+v", last)
	}
}

func TestSwarmSlashAppendsCard(t *testing.T) {
	m := testModel(t)
	if err := m.openSession(Options{CWD: t.TempDir(), Model: m.opts.Model}); err != nil {
		t.Fatal(err)
	}
	m.handleSwarm("split the work")
	var found *swarmCard
	for _, bl := range m.blocks {
		if bl.kind == blockSwarm {
			found = bl.swarm
		}
	}
	if found == nil || found.Desc != "split the work" || found.Expected != 1 {
		t.Fatalf("%+v", found)
	}
}

func TestWorkflowStartSyncsScrollback(t *testing.T) {
	m := testModel(t)
	run := workflowRun{
		ID: "wf-1", Name: "Ultracode", Objective: "ship it", Status: "active",
		Started: time.Now(),
		Phases:  builtinWorkflowPhases("ultracode"),
		Agents:  []workflowAgent{{State: "running"}},
	}
	m.syncWorkflowBlock(run)
	if len(m.blocks) == 0 || m.blocks[0].kind != blockWorkflow {
		t.Fatalf("%+v", m.blocks)
	}
	run.Status = "complete"
	run.Elapsed = 8 * time.Second
	m.syncWorkflowBlock(run)
	if len(m.blocks) != 1 {
		t.Fatalf("should update in place, got %d", len(m.blocks))
	}
	if m.blocks[0].workflow.Status != "complete" {
		t.Fatalf("status %q", m.blocks[0].workflow.Status)
	}
}

func TestNucleoMatchHighlightsAndRanks(t *testing.T) {
	score, marks := nucleoMatch("cmp", "compact the log")
	if score == 0 || len(marks) != 3 {
		t.Fatalf("score %d marks %v", score, marks)
	}
	if score, marks := nucleoMatch("zzz", "compact"); score != 0 || len(marks) != 0 {
		t.Fatalf("miss score %d marks %v", score, marks)
	}
	prefix, _ := nucleoMatch("comp", "compact")
	mid, _ := nucleoMatch("comp", "the compact log")
	if prefix <= mid {
		t.Fatalf("prefix %d should beat mid %d", prefix, mid)
	}
	items := fuzzyHistory("comp", []string{"compare hashes", "unrelated", "compact the log"})
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
	if len(items[0].Marks) == 0 {
		t.Fatalf("expected highlight marks on %q", items[0].Label)
	}
	hi := highlightMarks("compact", []int{0, 1, 2, 3}, func(s string) string { return "[" + s + "]" }, nil)
	if hi != "[c][o][m][p]act" {
		t.Fatalf("highlight %q", hi)
	}
}
