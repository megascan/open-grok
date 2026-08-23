package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

func TestParseSchedulerInterval(t *testing.T) {
	cases := map[string]uint64{
		"5m":     300,
		"10m":    600,
		"1m":     60,
		"2h":     7200,
		"1h":     3600,
		"1d":     86400,
		"7d":     604800,
		"30s":    60, // clamped
		"1s":     60,
		"60s":    60,
		"120s":   120,
		"  5m  ": 300,
	}
	for in, want := range cases {
		got, err := tools.ParseSchedulerInterval(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Fatalf("%q: got %d want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "5x", "m", "0m", "0s"} {
		if _, err := tools.ParseSchedulerInterval(bad); err == nil {
			t.Fatalf("%q should error", bad)
		}
	}
	if _, err := tools.ParseSchedulerInterval("1000000000000000000d"); err == nil {
		t.Fatal("overflow must error")
	}
}

func TestIntervalToHuman(t *testing.T) {
	if tools.IntervalToHuman(300) != "every 5 minutes" {
		t.Fatal(tools.IntervalToHuman(300))
	}
	if tools.IntervalToHuman(60) != "every 1 minute" {
		t.Fatal(tools.IntervalToHuman(60))
	}
	if tools.IntervalToHuman(3600) != "every 1 hour" {
		t.Fatal(tools.IntervalToHuman(3600))
	}
	if tools.IntervalToHuman(86400) != "every 1 day" {
		t.Fatal(tools.IntervalToHuman(86400))
	}
	if tools.IntervalToHuman(45) != "every 45 seconds" {
		t.Fatal(tools.IntervalToHuman(45))
	}
}

func TestSchedulerCreateUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	tc := tools.Context{SessionDir: dir}
	create, _ := json.Marshal(map[string]any{
		"interval": "5m", "prompt": "check deploy", "fire_immediately": true,
	})
	res := tools.SchedulerCreate{}.Execute(context.Background(), tc, create)
	if res.IsError {
		t.Fatal(res.Content)
	}
	var created struct {
		ID            string `json:"id"`
		HumanSchedule string `json:"humanSchedule"`
		Updated       bool   `json:"updated"`
	}
	if err := json.Unmarshal([]byte(res.Content), &created); err != nil {
		t.Fatal(err)
	}
	if created.Updated || created.HumanSchedule != "every 5 minutes" || created.ID == "" {
		t.Fatalf("%+v", created)
	}
	got := tools.LoadScheduledTasks(dir)
	if len(got) != 1 || got[0].Prompt != "check deploy" {
		t.Fatalf("%+v", got)
	}

	update, _ := json.Marshal(map[string]any{"task_id": created.ID, "interval": "10m"})
	res = tools.SchedulerCreate{}.Execute(context.Background(), tc, update)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, `"updated":true`) || !strings.Contains(res.Content, "every 10 minutes") {
		t.Fatalf("%s", res.Content)
	}
	if len(tools.LoadScheduledTasks(dir)) != 1 {
		t.Fatal("update must not create a second task")
	}

	missing, _ := json.Marshal(map[string]any{"task_id": "nonexistent", "prompt": "x"})
	res = tools.SchedulerCreate{}.Execute(context.Background(), tc, missing)
	if !res.IsError || !strings.Contains(res.Content, "no scheduled task with id") {
		t.Fatalf("%+v", res)
	}

	empty, _ := json.Marshal(map[string]any{"task_id": created.ID})
	res = tools.SchedulerCreate{}.Execute(context.Background(), tc, empty)
	if !res.IsError || !strings.Contains(res.Content, "nothing to update") {
		t.Fatalf("%+v", res)
	}

	oneshot, _ := json.Marshal(map[string]any{"interval": "5m", "prompt": "x", "recurring": false})
	res = tools.SchedulerCreate{}.Execute(context.Background(), tc, oneshot)
	if !res.IsError || !strings.Contains(res.Content, "sleep") {
		t.Fatalf("oneshot %s", res.Content)
	}

	del, _ := json.Marshal(map[string]string{"id": created.ID})
	res = tools.SchedulerDelete{}.Execute(context.Background(), tc, del)
	if res.IsError || !strings.Contains(res.Content, `"success":true`) {
		t.Fatalf("%s", res.Content)
	}
	if len(tools.LoadScheduledTasks(dir)) != 0 {
		t.Fatal("delete")
	}
	res = tools.SchedulerDelete{}.Execute(context.Background(), tc, del)
	if res.IsError || !strings.Contains(res.Content, `"success":false`) {
		t.Fatalf("missing delete %s", res.Content)
	}
}

func TestSchedulerAccessNone(t *testing.T) {
	if (tools.SchedulerCreate{}).Access() != types.AccessNone {
		t.Fatal("scheduler tools must not go through edit/bash gates")
	}
}
