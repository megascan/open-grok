// Package goals is the session GoalTracker: a pure-enough state machine
// persisted under session_dir/goal/. Unknown wire statuses restore as
// UserPaused so a corrupt snapshot can never resurrect as self-driving.
package goals

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	historyMax    = 64
	blockedNeeded = 3
	fileName      = "orchestration.json"
)

// Phase is the coarse harness phase.
type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhasePlanning  Phase = "planning"
	PhaseExecuting Phase = "executing"
)

// Status is the lifecycle status of a goal. Paused variants encode why.
type Status string

const (
	StatusActive           Status = "active"
	StatusUserPaused       Status = "user_paused"
	StatusBackOffPaused    Status = "back_off_paused"
	StatusNoProgressPaused Status = "no_progress_paused"
	StatusInfraPaused      Status = "infra_paused"
	StatusBlocked          Status = "blocked"
	StatusBudgetLimited    Status = "budget_limited"
	StatusComplete         Status = "complete"
)

// FromWire maps a persisted/wire status. Unknown values become UserPaused.
func FromWire(s string) Status {
	switch strings.TrimSpace(s) {
	case "active", "Active":
		return StatusActive
	case "user_paused", "paused", "Paused", "doom_loop_paused":
		return StatusUserPaused
	case "back_off_paused":
		return StatusBackOffPaused
	case "no_progress_paused":
		return StatusNoProgressPaused
	case "infra_paused":
		return StatusInfraPaused
	case "blocked":
		return StatusBlocked
	case "budget_limited", "BudgetLimited":
		return StatusBudgetLimited
	case "complete", "Complete":
		return StatusComplete
	default:
		return StatusUserPaused
	}
}

// IsPaused reports any paused variant (not Complete / BudgetLimited / Active).
func (s Status) IsPaused() bool {
	switch s {
	case StatusUserPaused, StatusBackOffPaused, StatusNoProgressPaused, StatusInfraPaused, StatusBlocked:
		return true
	default:
		return false
	}
}

// HistoryEntry is one capped goal-history row.
type HistoryEntry struct {
	Event  string    `json:"event"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Orchestration is the persisted snapshot (snake_case, rust-compatible names).
type Orchestration struct {
	GoalID            string         `json:"goal_id"`
	Objective         string         `json:"objective"`
	Status            Status         `json:"status"`
	Phase             Phase          `json:"phase"`
	TokenBudget       *int64         `json:"token_budget,omitempty"`
	ElapsedMS         int64          `json:"elapsed_ms"`
	CreatedAt         time.Time      `json:"created_at"`
	History           []HistoryEntry `json:"history,omitempty"`
	PauseMessage      string         `json:"pause_message,omitempty"`
	VerifierID        string         `json:"verifier_id"`
	PlanFile          string         `json:"plan_file,omitempty"`
	BlockedStreak     int            `json:"evaluator_blocked_streak,omitempty"`
	BlockerKey        string         `json:"evaluator_blocker_key,omitempty"`
	CompletionClaimed bool           `json:"completion_claimed,omitempty"`
	CompletionSummary string         `json:"completion_summary,omitempty"`
	Progress          []string       `json:"progress,omitempty"`
}

// UnmarshalJSON accepts unknown status strings as UserPaused.
func (o *Orchestration) UnmarshalJSON(data []byte) error {
	type raw Orchestration
	aux := struct {
		Status string `json:"status"`
		Phase  string `json:"phase"`
		*raw
	}{raw: (*raw)(o)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Status != "" {
		o.Status = FromWire(aux.Status)
	}
	switch strings.ToLower(aux.Phase) {
	case "planning":
		o.Phase = PhasePlanning
	case "executing":
		o.Phase = PhaseExecuting
	default:
		o.Phase = PhaseIdle
	}
	return nil
}

// Tracker is the session-owned goal state machine.
type Tracker struct {
	mu          sync.Mutex
	sessionDir  string
	o           *Orchestration
	activeSince *time.Time
}

// New constructs an empty tracker bound to a session directory.
func New(sessionDir string) *Tracker {
	return &Tracker{sessionDir: sessionDir}
}

// Load restores orchestration.json. In-flight Active becomes UserPaused;
// Planning/Executing reset to Idle because subagents do not survive restart.
func Load(sessionDir string) *Tracker {
	t := New(sessionDir)
	data, err := os.ReadFile(t.path())
	if err != nil {
		return t
	}
	var o Orchestration
	if err := json.Unmarshal(data, &o); err != nil {
		return t
	}
	o.Status = FromWire(string(o.Status))
	if o.Phase == PhasePlanning || o.Phase == PhaseExecuting {
		o.Phase = PhaseIdle
	}
	if o.Status == StatusActive {
		o.Status = StatusUserPaused
	}
	if o.VerifierID == "" || !canonicalVerifier(o.VerifierID) {
		o.VerifierID = newVerifierID()
	}
	t.o = &o
	return t
}

func canonicalVerifier(id string) bool {
	if len(id) != 12 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func (t *Tracker) path() string {
	return filepath.Join(t.sessionDir, "goal", fileName)
}

func (t *Tracker) goalDir() string {
	return filepath.Join(t.sessionDir, "goal")
}

// HasGoal reports whether any orchestration exists (including complete).
func (t *Tracker) HasGoal() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.o != nil
}

// IsActive reports StatusActive.
func (t *Tracker) IsActive() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.o != nil && t.o.Status == StatusActive
}

// Snapshot copies the current orchestration for display.
func (t *Tracker) Snapshot() *Orchestration {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil {
		return nil
	}
	cp := *t.o
	cp.History = append([]HistoryEntry(nil), t.o.History...)
	cp.Progress = append([]string(nil), t.o.Progress...)
	return &cp
}

// Create replaces any existing goal with a new Active/Executing one.
func (t *Tracker) Create(objective string, budget *int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return fmt.Errorf("goal objective is empty")
	}
	now := time.Now().UTC()
	id := newVerifierID()
	t.o = &Orchestration{
		GoalID:      id,
		Objective:   objective,
		Status:      StatusActive,
		Phase:       PhaseExecuting,
		TokenBudget: budget,
		CreatedAt:   now,
		VerifierID:  id,
		PlanFile:    filepath.Join(t.goalDir(), "plan.md"),
	}
	since := now
	t.activeSince = &since
	t.recordLocked("goal_created", "")
	if err := os.MkdirAll(t.goalDir(), 0o700); err != nil {
		return err
	}
	body := "# Goal\n\n" + objective + "\n\nAcceptance is user-verified. Call `update_goal` after meaningful progress.\n"
	_ = os.WriteFile(t.o.PlanFile, []byte(body), 0o600)
	return t.saveLocked()
}

// PauseUser applies /goal pause (Active → UserPaused).
func (t *Tracker) PauseUser() bool {
	return t.pause(StatusUserPaused, "user", "")
}

func (t *Tracker) pause(st Status, detail, message string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil || t.o.Status != StatusActive {
		return false
	}
	t.flushElapsedLocked()
	t.o.Status = st
	t.o.PauseMessage = message
	t.recordLocked("goal_paused", detail)
	_ = t.saveLocked()
	return true
}

// Resume re-arms any paused variant as Active.
func (t *Tracker) Resume() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil || !t.o.Status.IsPaused() {
		return false
	}
	t.o.Status = StatusActive
	t.o.Phase = PhaseExecuting
	t.o.PauseMessage = ""
	t.o.BlockedStreak = 0
	t.o.BlockerKey = ""
	now := time.Now().UTC()
	t.activeSince = &now
	t.recordLocked("goal_resumed", "")
	_ = t.saveLocked()
	return true
}

// UserComplete marks the goal Complete. The model cannot do this alone.
func (t *Tracker) UserComplete() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil {
		return false
	}
	if t.o.Status != StatusActive && !t.o.Status.IsPaused() {
		return false
	}
	t.flushElapsedLocked()
	t.o.Status = StatusComplete
	t.o.Phase = PhaseIdle
	t.o.PauseMessage = ""
	t.recordLocked("goal_completed", "user")
	_ = t.saveLocked()
	return true
}

// Clear drops orchestration.
func (t *Tracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.o = nil
	t.activeSince = nil
	_ = os.RemoveAll(t.goalDir())
}

// Save writes orchestration.json if a goal exists.
func (t *Tracker) Save() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil {
		return nil
	}
	return t.saveLocked()
}

func (t *Tracker) saveLocked() error {
	if t.o == nil {
		return nil
	}
	if err := os.MkdirAll(t.goalDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t.o, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.path(), data, 0o600)
}

func (t *Tracker) flushElapsedLocked() {
	if t.o == nil || t.activeSince == nil {
		return
	}
	t.o.ElapsedMS += time.Since(*t.activeSince).Milliseconds()
	t.activeSince = nil
}

func (t *Tracker) recordLocked(event, detail string) {
	if t.o == nil {
		return
	}
	t.o.History = append(t.o.History, HistoryEntry{Event: event, Detail: detail, At: time.Now().UTC()})
	if len(t.o.History) > historyMax {
		t.o.History = t.o.History[len(t.o.History)-historyMax:]
	}
}

func newVerifierID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(b[:])
}

// ApplyUpdate is the model-facing update_goal drain.
// completed:true is a claim, not auto-success — there is no independent
// classifier in this Go build.
func (t *Tracker) ApplyUpdate(completed bool, message, blocked string) (summary string, errCode string, err error) {
	if t == nil {
		return "", "goal_harness_disabled", fmt.Errorf("no goal is running; the user must start one with /goal <objective>")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.o == nil {
		return "", "goal_harness_disabled", fmt.Errorf("no goal is running; the user must start one with /goal <objective>")
	}
	if t.o.Status != StatusActive {
		code := "goal_update_non_active"
		if blocked != "" {
			code = "goal_update_blocked_against_non_active"
		} else if !completed && message != "" {
			code = "goal_update_message_against_non_active"
		}
		return "", code, fmt.Errorf("goal is %s; wait for the user to /goal resume", t.o.Status)
	}
	message = strings.TrimSpace(message)
	blocked = strings.TrimSpace(blocked)
	if completed {
		if message == "" {
			return "", "goal_update_non_active", fmt.Errorf("completed:true requires a non-empty message summary")
		}
		t.o.CompletionClaimed = true
		t.o.CompletionSummary = message
		t.o.Progress = append(t.o.Progress, message)
		t.recordLocked("completion_claimed", message)
		_ = t.saveLocked()
		return "completion claimed (independent verifier is not running in this build; the goal stays Active until the user runs /goal complete): " + message, "", nil
	}
	if blocked != "" {
		key := strings.ToLower(blocked)
		if t.o.BlockerKey == key {
			t.o.BlockedStreak++
		} else {
			t.o.BlockerKey = key
			t.o.BlockedStreak = 1
		}
		t.recordLocked("blocked_claim", blocked)
		if t.o.BlockedStreak >= blockedNeeded {
			t.flushElapsedLocked()
			t.o.Status = StatusBlocked
			t.o.PauseMessage = blocked
			t.recordLocked("goal_paused", "blocked")
			_ = t.saveLocked()
			return fmt.Sprintf("goal paused as blocked after %d consecutive claims: %s", blockedNeeded, blocked), "", nil
		}
		_ = t.saveLocked()
		return fmt.Sprintf("blocked claim %d/%d recorded: %s", t.o.BlockedStreak, blockedNeeded, blocked), "", nil
	}
	if message == "" {
		return "goal still active: " + t.o.Objective, "", nil
	}
	t.o.Progress = append(t.o.Progress, message)
	if len(t.o.Progress) > 32 {
		t.o.Progress = t.o.Progress[len(t.o.Progress)-32:]
	}
	t.recordLocked("goal_updated", message)
	_ = t.saveLocked()
	return "progress recorded: " + message, "", nil
}

// Reminder is the per-turn model-facing goal context.
func (t *Tracker) Reminder() string {
	snap := t.Snapshot()
	if snap == nil || snap.Status != StatusActive {
		return ""
	}
	var b strings.Builder
	b.WriteString("<goal_reminder>\n")
	fmt.Fprintf(&b, "Objective: %s\n", snap.Objective)
	fmt.Fprintf(&b, "Status: %s  phase: %s  id: %s\n", snap.Status, snap.Phase, snap.GoalID)
	if snap.PlanFile != "" {
		fmt.Fprintf(&b, "Plan file: %s\n", snap.PlanFile)
	}
	if snap.CompletionClaimed {
		fmt.Fprintf(&b, "A completion claim is pending user /goal complete. Summary: %s\n", snap.CompletionSummary)
	}
	b.WriteString("Call update_goal with a short message after meaningful progress.\n")
	b.WriteString("Set completed:true ONLY when fully achieved, together with a completion summary. That records a claim; it does not end goal mode by itself.\n")
	b.WriteString("Set blocked_reason only after 3+ consecutive failed attempts at the same problem. Never put success text there.\n")
	b.WriteString("</goal_reminder>\n")
	return b.String()
}

// StatusLine is a one-line TUI badge.
func (t *Tracker) StatusLine() string {
	snap := t.Snapshot()
	if snap == nil {
		return "goal off"
	}
	obj := snap.Objective
	if len(obj) > 40 {
		obj = obj[:37] + "…"
	}
	label := string(snap.Status)
	if snap.CompletionClaimed && snap.Status == StatusActive {
		label = "claimed"
	}
	return "goal " + label + " · " + obj
}
