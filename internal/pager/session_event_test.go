package pager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/megascan/open-grok/internal/agent"
)

func TestFormatDurationBuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "0.5s"},
		{time.Duration(5.23 * float64(time.Second)), "5.2s"},
		{10 * time.Second, "10s"},
		{time.Duration(12.3 * float64(time.Second)), "12s"},
		{125 * time.Second, "2m5s"},
		{3725 * time.Second, "1h2m"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.d); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.d, got, tc.want)
		}
	}
}

func TestSessionEventMessagesMatchRust(t *testing.T) {
	if got := (sessionEvent{kind: sessTurnCompleted, elapsed: 125 * time.Second, elapsedOK: true}).message(); got != "Worked for 2m5s" {
		t.Fatalf("completed: %q", got)
	}
	if got := (sessionEvent{kind: sessTurnCompleted}).message(); got != "Turn completed." {
		t.Fatalf("completed none: %q", got)
	}
	if got := (sessionEvent{kind: sessTurnCancelled, elapsed: 10 * time.Second}).message(); got != "Turn cancelled by user in 10s." {
		t.Fatalf("cancelled: %q", got)
	}
	if got := (sessionEvent{kind: sessTurnHalted, elapsed: 45 * time.Second}).message(); got != "Agent was unable to make progress "+emDash+" turn ended in 45s." {
		t.Fatalf("halted: %q", got)
	}
	if got := (sessionEvent{kind: sessTurnFailed, error: "connection reset", elapsed: 3 * time.Second, elapsedOK: true}).message(); got != "Turn failed in 3.0s: connection reset" {
		t.Fatalf("failed elapsed: %q", got)
	}
	if got := (sessionEvent{kind: sessTurnFailed, error: "auth error"}).message(); got != "Turn failed: auth error" {
		t.Fatalf("failed: %q", got)
	}
	if got := (sessionEvent{kind: sessGoalCompleted, elapsed: 619 * time.Second}).message(); got != "Goal complete "+emDash+" 10m19s end-to-end." {
		t.Fatalf("goal: %q", got)
	}
	before := uint64(48_800)
	ms := int64(21_000)
	if got := (sessionEvent{kind: sessCompactionCompleted, tokensBefore: &before, tokensAfter: 27_100, elapsedMS: &ms}).message(); got != "Context compacted: 48.8k → 27.1k tokens (21.0s)" {
		t.Fatalf("compact delta: %q", got)
	}
	if got := (sessionEvent{kind: sessCompactionCompleted, tokensAfter: 27_100}).message(); got != "Context compacted → 27.1k tokens" {
		t.Fatalf("compact legacy: %q", got)
	}
	if got := (sessionEvent{kind: sessCompactionFailed}).message(); got != "Compaction failed." {
		t.Fatalf("compact fail empty: %q", got)
	}
	if got := (sessionEvent{kind: sessRetryFailed, error: "raw API error message", errorType: "encrypted_content_mismatch"}).message(); got != encryptedHistoryMsg {
		t.Fatalf("encrypted: %q", got)
	}
	if got := (sessionEvent{kind: sessReAuthRequired}).message(); !strings.Contains(got, "/login") {
		t.Fatalf("reauth: %q", got)
	}
	if got := (sessionEvent{kind: sessContextTooLarge}).message(); !strings.Contains(got, "/new") {
		t.Fatalf("context: %q", got)
	}
	if got := (sessionEvent{kind: sessRequestFailed, headline: "Server error (500)", detail: "upstream exploded"}).message(); got != "Server error (500) "+emDash+" upstream exploded" {
		t.Fatalf("request: %q", got)
	}
	if got := (sessionEvent{kind: sessRecap, summary: "refactored the parser"}).message(); got != "Recap "+emDash+" refactored the parser" {
		t.Fatalf("recap: %q", got)
	}
	if got := (sessionEvent{kind: sessCompactCompleted, elapsed: 2 * time.Second, elapsedOK: true}).message(); got != "Compaction completed in 2.0s." {
		t.Fatalf("manual compact: %q", got)
	}
}

func TestSessionEventFromTurnClassifiesErrors(t *testing.T) {
	d := 4 * time.Second
	if ev := sessionEventFromTurn(nil, &d); ev.kind != sessTurnCompleted || ev.message() != "Worked for 4.0s" {
		t.Fatalf("ok: %+v %q", ev, ev.message())
	}
	if ev := sessionEventFromTurn(context.Canceled, &d); ev.kind != sessTurnCancelled {
		t.Fatalf("cancel: %+v", ev)
	}
	if ev := sessionEventFromTurn(errors.New("Unauthorized (401)"), &d); ev.kind != sessReAuthRequired {
		t.Fatalf("auth: %+v", ev)
	}
	if ev := sessionEventFromTurn(errors.New("max_prompt_length exceeded"), &d); ev.kind != sessContextTooLarge {
		t.Fatalf("context: %+v", ev)
	}
	if ev := sessionEventFromTurn(errors.New("max tool turns (32) exceeded"), &d); ev.kind != sessTurnHalted {
		t.Fatalf("halt: %+v", ev)
	}
	if ev := sessionEventFromTurn(errors.New("no space left on device"), &d); ev.kind != sessDiskFull {
		t.Fatalf("disk: %+v", ev)
	}
	if ev := sessionEventFromTurn(errors.New("boom"), &d); ev.kind != sessTurnFailed || !strings.Contains(ev.message(), "Turn failed in 4.0s") {
		t.Fatalf("fail: %q", ev.message())
	}
}

func TestTurnDoneAppendsWorkedForMarker(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.turnStarted = time.Now().Add(-125 * time.Second)
	m.busy = true
	m.Update(turnDone{})
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].kind != blockSession {
		t.Fatalf("blocks: %+v", m.blocks)
	}
	got := m.blocks[len(m.blocks)-1].event.message()
	if !strings.HasPrefix(got, "Worked for ") {
		t.Fatalf("marker %q", got)
	}
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Worked for ") {
		t.Fatalf("view:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Worked for") && strings.Contains(line, permAccentBar) {
			t.Fatalf("accent on completed marker: %q", line)
		}
	}
}

func TestTurnDoneCancelledAndFailedChrome(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.turnStarted = time.Now().Add(-10 * time.Second)
	m.Update(turnDone{err: context.Canceled})
	got := m.blocks[len(m.blocks)-1].event.message()
	if !strings.HasPrefix(got, "Turn cancelled by user in ") {
		t.Fatalf("cancel %q", got)
	}
	m2 := testModel(t)
	m2.width, m2.height = 100, 40
	m2.turnStarted = time.Now().Add(-3 * time.Second)
	m2.Update(turnDone{err: errors.New("connection reset")})
	view := stripANSI(m2.renderTranscript())
	if !strings.Contains(strings.ReplaceAll(view, "\n", " "), "Turn failed in 3.0s: connection reset") &&
		!strings.Contains(view, "Turn failed in 3.0s: connection reset") {
		t.Fatalf("failed view:\n%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("failed banner needs warning accent")
	}
}

func TestRecapRendersToolStyleHeader(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.appendSessionEvent(sessionEvent{kind: sessRecap, summary: "Refactored the parser and added tests."})
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Recap") {
		t.Fatalf("header:\n%s", view)
	}
	if !strings.Contains(view, "Refactored the parser and added tests.") {
		t.Fatalf("body:\n%s", view)
	}
	if strings.Contains(view, "Recap "+emDash+" Refactored") && !strings.Contains(view, "\n") {
		t.Fatal("expanded recap should not be a single Recap — line")
	}
	m.blocks[0].folded = true
	collapsed := stripANSI(m.renderSessionEvent(m.blocks[0], 80))
	if !strings.Contains(collapsed, "Recap") || !strings.Contains(collapsed, "Refactored the parser") {
		t.Fatalf("collapsed: %q", collapsed)
	}
	if strings.Contains(collapsed, permAccentBar) {
		t.Fatalf("collapsed recap has no accent: %q", collapsed)
	}
}

func TestRecapLoadingIsHeaderOnly(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.appendSessionEvent(sessionEvent{kind: sessRecap, running: true})
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "Recap") {
		t.Fatalf("%s", view)
	}
	if strings.Contains(view, "generating") {
		t.Fatalf("loading recap is header only:\n%s", view)
	}
}

func TestReAuthBannerHasWarningAccent(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.appendSessionEvent(sessionEvent{kind: sessReAuthRequired})
	view := stripANSI(m.renderTranscript())
	if !strings.Contains(view, "/login") {
		t.Fatalf("%s", view)
	}
	if !strings.Contains(view, permAccentBar) {
		t.Fatal("re-auth must use warning accent")
	}
}

func TestCompactSlashEmitsCompletedMarker(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	m.handleSlash("/compact")
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockSession || last.event.kind != sessCompactCompleted {
		t.Fatalf("%+v", last)
	}
	if !strings.HasPrefix(last.event.message(), "Compaction completed in ") {
		t.Fatalf("%q", last.event.message())
	}
}

func TestGoalCompleteEmitsEndToEndMarker(t *testing.T) {
	m := testModel(t)
	sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, false)
	if err != nil {
		t.Fatal(err)
	}
	m.sess = sess
	if err := m.sess.Goal.Create("land the TUI", nil); err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/goal complete")
	got := lastSys(m)
	if !strings.Contains(got, "Goal complete") || !strings.Contains(got, "end-to-end") {
		t.Fatalf("%q", got)
	}
}
