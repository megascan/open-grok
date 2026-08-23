package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/megascan/open-grok/internal/types"
)

const (
	schedulerFileName       = "scheduled_tasks.json"
	maxScheduledTasks       = 50
	recurringTaskTTL        = 7 * 24 * time.Hour
	minimumIntervalSecs     = 60
	schedulerCreateToolName = "scheduler_create"
	schedulerDeleteToolName = "scheduler_delete"
	schedulerListToolName   = "scheduler_list"
)

// ScheduledTask is one persisted /loop job (Rust grok_build scheduler).
type ScheduledTask struct {
	ID           string     `json:"id"`
	IntervalSecs uint64     `json:"interval_secs"`
	Prompt       string     `json:"prompt"`
	Recurring    bool       `json:"recurring"`
	Durable      bool       `json:"durable"`
	Foreground   bool       `json:"foreground"`
	CreatedAt    time.Time  `json:"created_at"`
	LastFiredAt  *time.Time `json:"last_fired_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	NextFireAt   time.Time  `json:"next_fire_at"`
}

type schedulerCreateArgs struct {
	TaskID          *string `json:"task_id"`
	Interval        *string `json:"interval"`
	Prompt          *string `json:"prompt"`
	Recurring       *bool   `json:"recurring"`
	Durable         *bool   `json:"durable"`
	Foreground      *bool   `json:"foreground"`
	FireImmediately bool    `json:"fire_immediately"`
}

type schedulerCreateOut struct {
	ID            string `json:"id"`
	HumanSchedule string `json:"humanSchedule"`
	Updated       bool   `json:"updated"`
}

type schedulerDeleteOut struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type scheduledTaskSummary struct {
	ID            string `json:"id"`
	Prompt        string `json:"prompt"`
	IntervalHuman string `json:"intervalHuman"`
	NextFireAt    string `json:"nextFireAt"`
	CreatedAt     string `json:"createdAt"`
	Recurring     bool   `json:"recurring"`
}

type schedulerListOut struct {
	Tasks []scheduledTaskSummary `json:"tasks"`
}

var (
	schedMu    sync.Mutex
	schedCache = map[string][]ScheduledTask{}
)

// SchedulerCreate schedules a recurring prompt, or patches an existing task.
type SchedulerCreate struct{}

func (SchedulerCreate) Name() string { return schedulerCreateToolName }
func (SchedulerCreate) Description() string {
	return "Create a scheduled task that runs a prompt on a recurring interval, or update an existing one in place.\n\n" +
		"Set fire_immediately: true to also fire once on creation; by default the first run waits for the interval.\n\n" +
		"To change an existing task, pass its task_id: provided fields replace old values, omitted ones are unchanged, and the schedule keeps its phase. An unknown id errors.\n\n" +
		"Usage notes:\n" +
		"- Interval format: \"5m\" (minutes), \"2h\" (hours), \"1d\" (days), \"60s\" (seconds, min 60)\n" +
		"- Maximum 50 scheduled tasks at once\n" +
		"- Tasks auto-expire after 7 days\n" +
		"- For one-time delayed work, run a background terminal command (e.g. `sleep 1800 && <command>`) instead; its completion notifies you"
}
func (SchedulerCreate) Access() types.AccessKind { return types.AccessNone }
func (SchedulerCreate) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"task_id": map[string]any{
			"type":        "string",
			"description": "Id of an existing task to update in place: provided fields replace old values, omitted ones are unchanged, the schedule keeps its phase, and an unknown id errors. Omit to create a task.",
		},
		"interval": map[string]any{
			"type":        "string",
			"description": "Interval between executions, e.g. \"5m\", \"2h\", \"1d\". Required to create; optional with task_id",
		},
		"prompt": map[string]any{
			"type":        "string",
			"description": "The prompt text to execute on each scheduled fire. Required to create; optional with task_id",
		},
		"durable": map[string]any{
			"type":        "boolean",
			"description": "Whether the task persists across sessions. Default: false. Create-only: ignored with task_id",
		},
		"foreground": map[string]any{
			"type":        "boolean",
			"description": "Run each fire as a main-conversation turn instead of a background subagent; set true only when runs need the conversation's context. Default: false. Create-only: ignored with task_id",
		},
		"fire_immediately": map[string]any{
			"type":        "boolean",
			"description": "Whether to fire immediately on creation (true) or wait for the first interval (false). Default: false. Create-only: ignored with task_id",
		},
	}, nil)
}

func (SchedulerCreate) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	var in schedulerCreateArgs
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return errResult("invalid scheduler_create args: " + err.Error())
		}
	}
	if in.TaskID != nil && strings.TrimSpace(*in.TaskID) != "" {
		return updateScheduledTask(tc.SessionDir, strings.TrimSpace(*in.TaskID), in)
	}
	if in.Recurring != nil && !*in.Recurring {
		return errResult("one-shot tasks are not supported; run a background terminal command instead (`sleep <secs> && <command>`, background: true) or do the work now")
	}
	if in.Interval == nil || strings.TrimSpace(*in.Interval) == "" {
		return errResult("interval is required when creating a task")
	}
	if in.Prompt == nil || strings.TrimSpace(*in.Prompt) == "" {
		return errResult("prompt is required when creating a task")
	}
	secs, err := ParseSchedulerInterval(strings.TrimSpace(*in.Interval))
	if err != nil {
		return errResult(err.Error())
	}
	if tc.SessionDir == "" {
		return errResult("scheduler_create requires a session directory")
	}
	now := time.Now().UTC()
	expires := now.Add(recurringTaskTTL)
	next := now.Add(time.Duration(secs) * time.Second)
	if in.FireImmediately {
		next = now
	}
	task := ScheduledTask{
		ID:           newSchedulerID(),
		IntervalSecs: secs,
		Prompt:       strings.TrimSpace(*in.Prompt),
		Recurring:    true,
		Durable:      in.Durable != nil && *in.Durable,
		Foreground:   in.Foreground != nil && *in.Foreground,
		CreatedAt:    now,
		ExpiresAt:    &expires,
		NextFireAt:   next,
	}
	state := loadScheduled(tc.SessionDir)
	if len(state) >= maxScheduledTasks {
		return errResult(fmt.Sprintf("maximum of %d scheduled tasks reached", maxScheduledTasks))
	}
	state = append(state, task)
	if err := saveScheduled(tc.SessionDir, state); err != nil {
		return errResult(err.Error())
	}
	return jsonResult(schedulerCreateOut{
		ID:            task.ID,
		HumanSchedule: IntervalToHuman(secs),
		Updated:       false,
	})
}

func updateScheduledTask(sessionDir, id string, in schedulerCreateArgs) Result {
	if in.Prompt == nil && in.Interval == nil {
		return errResult("nothing to update: provide interval and/or prompt alongside task_id")
	}
	state := loadScheduled(sessionDir)
	idx := -1
	for i := range state {
		if state[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errResult("no scheduled task with id " + id)
	}
	if in.Interval != nil {
		secs, err := ParseSchedulerInterval(strings.TrimSpace(*in.Interval))
		if err != nil {
			return errResult(err.Error())
		}
		state[idx].IntervalSecs = secs
	}
	if in.Prompt != nil {
		state[idx].Prompt = strings.TrimSpace(*in.Prompt)
	}
	if err := saveScheduled(sessionDir, state); err != nil {
		return errResult(err.Error())
	}
	return jsonResult(schedulerCreateOut{
		ID:            state[idx].ID,
		HumanSchedule: IntervalToHuman(state[idx].IntervalSecs),
		Updated:       true,
	})
}

// SchedulerDelete cancels a scheduled task by ID.
type SchedulerDelete struct{}

func (SchedulerDelete) Name() string { return schedulerDeleteToolName }
func (SchedulerDelete) Description() string {
	return "Cancel a scheduled task by ID.\n\nReturns success: true if the task was found and removed, false if no task with that ID exists."
}
func (SchedulerDelete) Access() types.AccessKind { return types.AccessNone }
func (SchedulerDelete) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"id": map[string]any{
			"type":        "string",
			"description": "The task ID to cancel (from scheduler_create output)",
		},
	}, []string{"id"})
}

func (SchedulerDelete) Execute(_ context.Context, tc Context, args json.RawMessage) Result {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return errResult("invalid scheduler_delete args: " + err.Error())
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return errResult("id is required")
	}
	state := loadScheduled(tc.SessionDir)
	kept := state[:0]
	found := false
	for _, t := range state {
		if t.ID == id {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return jsonResult(schedulerDeleteOut{
			Success: false,
			Message: fmt.Sprintf("No scheduled task with ID %s found. Use scheduler_list to see active tasks.", id),
		})
	}
	if err := saveScheduled(tc.SessionDir, kept); err != nil {
		return errResult(err.Error())
	}
	return jsonResult(schedulerDeleteOut{
		Success: true,
		Message: fmt.Sprintf("Scheduled task %s cancelled.", id),
	})
}

// SchedulerList lists active scheduled tasks.
type SchedulerList struct{}

func (SchedulerList) Name() string { return schedulerListToolName }
func (SchedulerList) Description() string {
	return "List all active scheduled tasks with their IDs, prompts, intervals, and next fire times."
}
func (SchedulerList) Access() types.AccessKind { return types.AccessNone }
func (SchedulerList) Parameters() map[string]any {
	return objectSchema(map[string]any{}, nil)
}

func (SchedulerList) Execute(_ context.Context, tc Context, _ json.RawMessage) Result {
	state := loadScheduled(tc.SessionDir)
	out := schedulerListOut{Tasks: make([]scheduledTaskSummary, 0, len(state))}
	for _, t := range state {
		prompt := t.Prompt
		if len(prompt) > 80 {
			prompt = prompt[:80] + "..."
		}
		out.Tasks = append(out.Tasks, scheduledTaskSummary{
			ID:            t.ID,
			Prompt:        prompt,
			IntervalHuman: IntervalToHuman(t.IntervalSecs),
			NextFireAt:    t.NextFireAt.UTC().Format(time.RFC3339),
			CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
			Recurring:     t.Recurring,
		})
	}
	return jsonResult(out)
}

// ParseSchedulerInterval converts "5m"/"2h"/"1d"/"60s" into seconds.
// Values below 60 seconds are raised. Matches the Rust parse_interval contract.
func ParseSchedulerInterval(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("interval cannot be empty")
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid interval format: %q (expected e.g. 5m, 2h, 1d)", s)
	}
	digits, suffix := s[:len(s)-1], s[len(s)-1:]
	for _, r := range digits {
		if !unicode.IsDigit(r) {
			return 0, fmt.Errorf("invalid interval format: %q (expected e.g. 5m, 2h, 1d)", s)
		}
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid interval format: %q (expected e.g. 5m, 2h, 1d)", s)
	}
	if value == 0 {
		return 0, fmt.Errorf("interval value must be greater than 0")
	}
	var unit uint64
	switch suffix {
	case "s":
		unit = 1
	case "m":
		unit = 60
	case "h":
		unit = 3600
	case "d":
		unit = 86400
	default:
		return 0, fmt.Errorf("invalid interval suffix: %q (expected s, m, h, or d)", suffix)
	}
	secs, overflow := mulOverflow(value, unit)
	if overflow {
		return 0, fmt.Errorf("interval too large: %q", s)
	}
	if secs < minimumIntervalSecs {
		secs = minimumIntervalSecs
	}
	return secs, nil
}

func mulOverflow(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	out := a * b
	if out/a != b {
		return 0, true
	}
	return out, false
}

// IntervalToHuman matches the Rust interval_to_human helper.
func IntervalToHuman(secs uint64) string {
	switch {
	case secs%86400 == 0:
		n := secs / 86400
		if n == 1 {
			return "every 1 day"
		}
		return fmt.Sprintf("every %d days", n)
	case secs%3600 == 0:
		n := secs / 3600
		if n == 1 {
			return "every 1 hour"
		}
		return fmt.Sprintf("every %d hours", n)
	case secs%60 == 0:
		n := secs / 60
		if n == 1 {
			return "every 1 minute"
		}
		return fmt.Sprintf("every %d minutes", n)
	case secs == 1:
		return "every 1 second"
	default:
		return fmt.Sprintf("every %d seconds", secs)
	}
}

// LoadScheduledTasks returns persisted /loop jobs for the pager ticker.
func LoadScheduledTasks(sessionDir string) []ScheduledTask {
	return loadScheduled(sessionDir)
}

// MarkScheduledFire records that a loop fired and advances next_fire_at.
func MarkScheduledFire(sessionDir, id string, at time.Time) error {
	state := loadScheduled(sessionDir)
	found := false
	for i := range state {
		if state[i].ID != id {
			continue
		}
		found = true
		t := at.UTC()
		state[i].LastFiredAt = &t
		state[i].NextFireAt = t.Add(time.Duration(state[i].IntervalSecs) * time.Second)
	}
	if !found {
		return fmt.Errorf("no scheduled task with id %s", id)
	}
	return saveScheduled(sessionDir, state)
}

func loadScheduled(sessionDir string) []ScheduledTask {
	if sessionDir == "" {
		return nil
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	if cached, ok := schedCache[sessionDir]; ok {
		out := make([]ScheduledTask, len(cached))
		copy(out, cached)
		return out
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, schedulerFileName))
	if err != nil {
		return nil
	}
	var items []ScheduledTask
	if json.Unmarshal(data, &items) != nil {
		return nil
	}
	schedCache[sessionDir] = items
	out := make([]ScheduledTask, len(items))
	copy(out, items)
	return out
}

func saveScheduled(sessionDir string, items []ScheduledTask) error {
	if sessionDir == "" {
		return fmt.Errorf("scheduler_create requires a session directory")
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sessionDir, schedulerFileName), data, 0o600); err != nil {
		return err
	}
	schedMu.Lock()
	cloned := make([]ScheduledTask, len(items))
	copy(cloned, items)
	schedCache[sessionDir] = cloned
	schedMu.Unlock()
	return nil
}

func newSchedulerID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:12]
	}
	return hex.EncodeToString(b[:])[:12]
}

func jsonResult(v any) Result {
	data, err := json.Marshal(v)
	if err != nil {
		return errResult(err.Error())
	}
	return okResult(string(data))
}
