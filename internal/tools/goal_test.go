package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/goals"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestUpdateGoalHarnessDisabled(t *testing.T) {
	res := tools.UpdateGoal{}.Execute(context.Background(), tools.Context{}, json.RawMessage(`{"message":"hi"}`))
	if !res.IsError || !strings.Contains(res.Content, "goal_harness_disabled") {
		t.Fatalf("got %q", res.Content)
	}
}

func TestUpdateGoalProgressWhenActive(t *testing.T) {
	tr := goals.New(t.TempDir())
	if err := tr.Create("visual TUI", nil); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"message": "drew the rail"})
	res := tools.UpdateGoal{}.Execute(context.Background(), tools.Context{Goal: tr}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "progress") {
		t.Fatalf("%s", res.Content)
	}
}

func TestUpdateGoalAccessNone(t *testing.T) {
	var ug tools.UpdateGoal
	if ug.Access() != types.AccessNone {
		t.Fatal("update_goal must not go through edit/bash gates")
	}
}

func TestPacksIncludeUpdateGoal(t *testing.T) {
	foundDefault, foundDirect := false, false
	for _, ttool := range tools.DefaultPack() {
		if ttool.Name() == "update_goal" {
			foundDefault = true
		}
	}
	for _, ttool := range tools.DirectOnlyPack() {
		if ttool.Name() == "update_goal" {
			foundDirect = true
		}
	}
	if !foundDefault || !foundDirect {
		t.Fatalf("update_goal must be in DefaultPack and DirectOnlyPack (top-level in Code Mode Only)")
	}
	for _, ttool := range tools.NestedPack() {
		if ttool.Name() == "update_goal" {
			t.Fatal("update_goal must not be nested under tools.*")
		}
	}
}
