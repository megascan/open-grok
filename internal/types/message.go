package types

// Role is a conversation message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a provider-neutral conversation turn.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a model-requested client tool invocation.
type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec is the JSON-schema function definition sent to the model.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// StreamEvent is one sampler increment (text, thinking, or tool call).
type StreamEvent struct {
	Kind      StreamKind
	Text      string
	Thinking  string
	ToolCall  *ToolCall
	Usage     *Usage
	Finish    string
	Err       error
}

// StreamKind classifies a StreamEvent.
type StreamKind int

const (
	StreamDelta StreamKind = iota
	StreamThinking
	StreamToolCall
	StreamUsage
	StreamDone
	StreamError
)

// Usage is token accounting for a completed sample.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SampleRequest is the provider-neutral request the sampler adapters consume.
type SampleRequest struct {
	Model            string
	Messages         []Message
	Tools            []ToolSpec
	Temperature      *float32
	MaxTokens        *int
	ReasoningEffort  string
	ConversationID   string
	SessionID        string
	TurnIndex        int
}

// AccessKind classifies a tool for the permission / plan-mode pipeline.
type AccessKind string

const (
	AccessRead AccessKind = "read"
	AccessEdit AccessKind = "edit"
	AccessBash AccessKind = "bash"
	AccessMCP  AccessKind = "mcp"
	AccessWeb  AccessKind = "web"
	AccessNone AccessKind = "none"
)
