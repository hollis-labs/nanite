package provider

import "context"

// StreamEvent represents a single event from a streaming provider response.
type StreamEvent struct {
	Type    string // "delta", "tool_use", "usage", "error", "done"
	Content string // text delta
	Usage   *Usage // only on "usage" or "done" events
	Error   string // only on "error" events
}

// Usage contains token usage information.
type Usage struct {
	InputTokens  int
	OutputTokens int
	StopReason   string
}

// ChatMessage represents a single message in a conversation.
type ChatMessage struct {
	Role    string
	Content string
}

// Provider is the interface for LLM provider adapters.
type Provider interface {
	StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error)
	// Complete makes a simple non-streaming completion call.
	Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error)
}
