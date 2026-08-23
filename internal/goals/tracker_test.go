package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownWireStatusRestoresUserPaused(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goal")
	if err := os.MkdirAll(goalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"goal_id":"abc","objective":"ship it","status":"future_self_driving","phase":"executing","verifier_id":"aabbccddeeff"}`
	if err := os.WriteFile(filepath.Join(goalDir, fileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := Load(dir)
	snap := tr.Snapshot()
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.Status != StatusUserPaused {
		t.Fatalf("unknown wire status must restore UserPaused, got %s", snap.Status)
	}
	if snap.Phase != PhaseIdle {
		t.Fatalf("in-flight phase must reset to idle, got %s", snap.Phase)
	}
}

func TestActiveSnapshotPausesOnLoad(t *testing.T) {
	dir := t.TempDir()
	tr := New(dir)
	if err := tr.Create("migrate auth", nil); err != nil {
		t.Fatal(err)
	}
	if !tr.IsActive() {
		t.Fatal("fresh goal must be active")
	}
	loaded := Load(dir)
	if loaded.IsActive() {
		t.Fatal("Active must become UserPaused on restore (subagents do not survive restart)")
	}
	if got := loaded.Snapshot().Status; got != StatusUserPaused {
		t.Fatalf("got %s", got)
	}
}

func TestPauseResumeClear(t *testing.T) {
	tr := New(t.TempDir())
	if err := tr.Create("do the thing", nil); err != nil {
		t.Fatal(err)
	}
	if !tr.PauseUser() {
		t.Fatal("pause")
	}
	if tr.IsActive() {
		t.Fatal("paused")
	}
	if !tr.Resume() {
		t.Fatal("resume")
	}
	if !tr.IsActive() {
		t.Fatal("active after resume")
	}
	tr.Clear()
	if tr.HasGoal() {
		t.Fatal("cleared")
	}
}

func TestUpdateGoalRejectsWhenHarnessDisabled(t *testing.T) {
	tr := New(t.TempDir())
	_, code, err := tr.ApplyUpdate(false, "progress", "")
	if err == nil || code != "goal_harness_disabled" {
		t.Fatalf("code=%s err=%v", code, err)
	}
}

func TestUpdateGoalProgressAndBlockedStreak(t *testing.T) {
	tr := New(t.TempDir())
	if err := tr.Create("land TUI", nil); err != nil {
		t.Fatal(err)
	}
	sum, code, err := tr.ApplyUpdate(false, "drew the header", "")
	if err != nil || code != "" || !strings.Contains(sum, "progress") {
		t.Fatalf("%s %s %v", sum, code, err)
	}
	for i := 0; i < blockedNeeded-1; i++ {
		_, _, err := tr.ApplyUpdate(false, "", "no compiler")
		if err != nil {
			t.Fatal(err)
		}
		if !tr.IsActive() {
			t.Fatal("should stay active before 3 blocked claims")
		}
	}
	sum, _, err = tr.ApplyUpdate(false, "", "no compiler")
	if err != nil {
		t.Fatal(err)
	}
	if tr.IsActive() {
		t.Fatal("third blocked claim must pause")
	}
	if got := tr.Snapshot().Status; got != StatusBlocked {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(sum, "blocked") {
		t.Fatalf("summary %s", sum)
	}
}

func TestCompletedIsClaimNotAutoSuccess(t *testing.T) {
	tr := New(t.TempDir())
	if err := tr.Create("finish", nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := tr.ApplyUpdate(true, "", "")
	if err == nil {
		t.Fatal("empty completion message must reject")
	}
	sum, _, err := tr.ApplyUpdate(true, "all tests pass", "")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsActive() {
		t.Fatal("model completed:true must not mark Complete")
	}
	if !tr.Snapshot().CompletionClaimed {
		t.Fatal("claim flag")
	}
	if !strings.Contains(sum, "claimed") {
		t.Fatalf("summary %s", sum)
	}
	if !tr.UserComplete() {
		t.Fatal("user complete")
	}
	if got := tr.Snapshot().Status; got != StatusComplete {
		t.Fatalf("got %s", got)
	}
}

func TestJSONRoundTripSnakeCase(t *testing.T) {
	dir := t.TempDir()
	tr := New(dir)
	if err := tr.Create("obj", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "goal", fileName))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["status"] != "active" {
		t.Fatalf("status %v", m["status"])
	}
	if m["phase"] != "executing" {
		t.Fatalf("phase %v", m["phase"])
	}
}
