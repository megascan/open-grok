package tools

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// AgentInfo is one child in the flat-team roster.
type AgentInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
}

// TeamBox is the in-process mailbox children use without seeing peer transcripts.
type TeamBox struct {
	mu     sync.Mutex
	agents map[string]AgentInfo
	mail   map[string][]string
	done   map[string]chan struct{}
	follow func(ctx context.Context, id, prompt string) Result
}

// NewTeamBox constructs an empty mailbox.
func NewTeamBox() *TeamBox {
	return &TeamBox{
		agents: map[string]AgentInfo{},
		mail:   map[string][]string{},
		done:   map[string]chan struct{}{},
	}
}

// Register adds a child. Status starts as "running".
func (b *TeamBox) Register(id, label string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.agents[id] = AgentInfo{ID: id, Label: label, Status: "running"}
	b.done[id] = make(chan struct{})
	b.mu.Unlock()
}

// Finish records a child's terminal result.
func (b *TeamBox) Finish(id, result string, failed bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	info := b.agents[id]
	info.Result = result
	if failed {
		info.Status = "failed"
	} else {
		info.Status = "idle"
	}
	b.agents[id] = info
	ch := b.done[id]
	b.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

// SetFollowup installs the runtime callback used by followup_task.
func (b *TeamBox) SetFollowup(fn func(ctx context.Context, id, prompt string) Result) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.follow = fn
	b.mu.Unlock()
}

func (b *TeamBox) snapshot() []AgentInfo {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]AgentInfo, 0, len(b.agents))
	for _, a := range b.agents {
		out = append(out, a)
	}
	return out
}

func (b *TeamBox) send(id, body string) error {
	if b == nil {
		return errNoTeam
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.agents[id]; !ok {
		return errUnknownAgent
	}
	b.mail[id] = append(b.mail[id], body)
	return nil
}

func (b *TeamBox) wait(id string, timeout time.Duration) (AgentInfo, bool) {
	if b == nil {
		return AgentInfo{}, false
	}
	b.mu.Lock()
	ch := b.done[id]
	info, ok := b.agents[id]
	b.mu.Unlock()
	if !ok {
		return AgentInfo{}, false
	}
	if info.Status != "running" || ch == nil {
		return info, true
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	select {
	case <-ch:
		b.mu.Lock()
		info = b.agents[id]
		b.mu.Unlock()
		return info, true
	case <-time.After(timeout):
		return info, false
	}
}

var (
	errNoTeam       = jsonError("team mailbox is not bound")
	errUnknownAgent = jsonError("unknown agent id")
)

type jsonError string

func (e jsonError) Error() string { return string(e) }

// ListAgents lists live children of this session.
type ListAgents struct{ Box *TeamBox }

func (ListAgents) Name() string { return "list_agents" }
func (ListAgents) Description() string {
	return "List live child agents spawned by this session (flat team). Direct-only."
}
func (ListAgents) Access() types.AccessKind { return types.AccessNone }
func (ListAgents) Parameters() map[string]any {
	return objectSchema(map[string]any{}, nil)
}
func unboundTeam() Result {
	return errResult("team mailbox is not bound")
}

func (t ListAgents) Execute(_ context.Context, _ Context, _ json.RawMessage) Result {
	if t.Box == nil {
		return unboundTeam()
	}
	list := t.Box.snapshot()
	if len(list) == 0 {
		return okResult("no child agents")
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	return okResult(string(data))
}

// SendMessage posts to a child's mailbox without exposing transcripts.
type SendMessage struct{ Box *TeamBox }

func (SendMessage) Name() string { return "send_message" }
func (SendMessage) Description() string {
	return "Send a steering message to a live child agent. Direct-only."
}
func (SendMessage) Access() types.AccessKind { return types.AccessNone }
func (SendMessage) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"agent_id": map[string]any{"type": "string"},
		"message":  map[string]any{"type": "string"},
	}, []string{"agent_id", "message"})
}
func (t SendMessage) Execute(_ context.Context, _ Context, args json.RawMessage) Result {
	if t.Box == nil {
		return unboundTeam()
	}
	m := unmarshal(args)
	if err := t.Box.send(str(m["agent_id"]), str(m["message"])); err != nil {
		return errResult(err.Error())
	}
	return okResult("queued")
}

// FollowupTask sends another prompt to an existing child.
type FollowupTask struct{ Box *TeamBox }

func (FollowupTask) Name() string { return "followup_task" }
func (FollowupTask) Description() string {
	return "Send a follow-up prompt to an existing child agent (max depth 1). Direct-only."
}
func (FollowupTask) Access() types.AccessKind { return types.AccessNone }
func (FollowupTask) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"agent_id": map[string]any{"type": "string"},
		"prompt":   map[string]any{"type": "string"},
	}, []string{"agent_id", "prompt"})
}
func (t FollowupTask) Execute(ctx context.Context, _ Context, args json.RawMessage) Result {
	if t.Box == nil {
		return errResult("team mailbox is not bound")
	}
	t.Box.mu.Lock()
	fn := t.Box.follow
	t.Box.mu.Unlock()
	if fn == nil {
		return errResult("followup_task is not bound in this session")
	}
	m := unmarshal(args)
	return fn(ctx, str(m["agent_id"]), str(m["prompt"]))
}

// WaitAgent waits until a child finishes or times out.
type WaitAgent struct{ Box *TeamBox }

func (WaitAgent) Name() string { return "wait_agent" }
func (WaitAgent) Description() string {
	return "Wait for a child agent to finish. Direct-only."
}
func (WaitAgent) Access() types.AccessKind { return types.AccessNone }
func (WaitAgent) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"agent_id": map[string]any{"type": "string"},
		"timeout":  map[string]any{"type": "number"},
	}, []string{"agent_id"})
}
func (t WaitAgent) Execute(_ context.Context, _ Context, args json.RawMessage) Result {
	if t.Box == nil {
		return unboundTeam()
	}
	m := unmarshal(args)
	timeout := 30 * time.Second
	if v, ok := m["timeout"].(float64); ok && v > 0 {
		timeout = time.Duration(v * float64(time.Second))
	}
	info, ok := t.Box.wait(str(m["agent_id"]), timeout)
	if !ok {
		return errResult("timed out waiting for " + str(m["agent_id"]))
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	return okResult(string(data))
}
