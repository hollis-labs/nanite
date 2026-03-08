package provider

import "context"

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolUseBlock represents a tool_use content block from the LLM.
type ToolUseBlock struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ContentBlock represents a content block in a multi-block message.
// NOTE: Input uses a pointer to distinguish "absent" from "empty object".
// Anthropic requires the input field on tool_use blocks even when empty.
type ContentBlock struct {
	Type      string          `json:"type"`                    // text, tool_use, tool_result
	Text      string          `json:"text,omitempty"`          // text block
	ID        string          `json:"id,omitempty"`            // tool_use block ID
	Name      string          `json:"name,omitempty"`          // tool_use tool name
	Input     *map[string]any `json:"input,omitempty"`         // tool_use input (always set for tool_use blocks)
	ToolUseID string          `json:"tool_use_id,omitempty"`   // tool_result reference
	Content   string          `json:"content,omitempty"`       // tool_result text
}

// StreamEvent represents a single event from a streaming provider response.
type StreamEvent struct {
	Type    string        // "delta", "tool_use", "usage", "error", "done"
	Content string        // text delta
	Usage   *Usage        // only on "usage" or "done" events
	Error   string        // only on "error" events
	ToolUse *ToolUseBlock // only on "tool_use" events
}

// Usage contains token usage information.
type Usage struct {
	InputTokens  int
	OutputTokens int
	StopReason   string
}

// ChatMessage represents a single message in a conversation.
// For simple text messages, use the Content field.
// For multi-block messages (e.g. tool_result), use ContentBlocks.
type ChatMessage struct {
	Role          string         `json:"role"`
	Content       string         `json:"content,omitempty"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
}

// Provider is the interface for LLM provider adapters.
type Provider interface {
	StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error)
	StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error)
	// Complete makes a simple non-streaming completion call.
	Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error)
}
