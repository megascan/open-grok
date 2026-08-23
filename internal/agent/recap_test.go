package agent

import (
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/types"
)

func TestCleanRecapText(t *testing.T) {
	if got := CleanRecapText("Refactored   the\n\nparser\tand added   tests."); got != "Refactored the parser and added tests." {
		t.Fatalf("collapse: %q", got)
	}
	if got := CleanRecapText("Recap: fixed the auth bug"); got != "fixed the auth bug" {
		t.Fatalf("label: %q", got)
	}
	if got := CleanRecapText("\"did the thing\""); got != "did the thing" {
		t.Fatalf("quotes: %q", got)
	}
	if got := FormatRecapLine("We fixed auth in foo.rs."); got != "Recap — We fixed auth in foo.rs." {
		t.Fatalf("format: %q", got)
	}
}

func TestRecapInstructionTag(t *testing.T) {
	text := RecapInstruction("system-reminder")
	if !strings.Contains(text, "<system-reminder>") || !strings.Contains(text, "</system-reminder>") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "Output ONLY the body") || !strings.Contains(text, "You asked") {
		t.Fatal("instruction body")
	}
	if strings.Contains(text, "217584") {
		t.Fatal("must stay synthetic")
	}
}

func TestCountUserMessagesSkipsReminders(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "hello"},
		{Role: types.RoleAssistant, Content: "hi"},
		{Role: types.RoleUser, Content: "<system-reminder>ignore</system-reminder>"},
		{Role: types.RoleUser, Content: "again"},
	}
	if n := CountUserMessages(msgs); n != 2 {
		t.Fatalf("count %d", n)
	}
}

func TestSnapshotForRecapDropsTrailingToolRun(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "hi"},
		{Role: types.RoleAssistant, Content: "", ToolCalls: []types.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: types.RoleTool, Content: "ok", ToolCallID: "c1"},
	}
	out := SnapshotForRecap(msgs)
	if len(out) != 2 {
		t.Fatalf("len %d %+v", len(out), out)
	}
	if out[0].Role != types.RoleUser || out[1].Role != types.RoleUser {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(out[1].Content, "<system-reminder>") {
		t.Fatal("instruction missing")
	}
}

func TestRecapToasts(t *testing.T) {
	if RecapUnavailableToast(false) != "No messages yet" {
		t.Fatal("empty")
	}
	if RecapUnavailableToast(true) != "Couldn't generate recap" {
		t.Fatal("fail")
	}
	if NoSessionNotice() != "No active session" {
		t.Fatal("session")
	}
}
