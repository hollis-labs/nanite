package provider

import (
	"encoding/json"
	"fmt"
)

// Claude Code stream-json event types.
// See: claude -p "..." --output-format stream-json --verbose

// claudeEvent is the top-level envelope for all stream-json events.
type claudeEvent struct {
	Type    string `json:"type"`    // "system", "assistant", "result", "rate_limit_event", "error"
	Subtype string `json:"subtype"` // e.g. "init", "success"
}

// claudeAssistantEvent is an "assistant" event wrapping a message object.
type claudeAssistantEvent struct {
	Type    string              `json:"type"`
	Message claudeAssistantMsg  `json:"message"`
}

type claudeAssistantMsg struct {
	Role    string               `json:"role"`
	Content []claudeContentBlock `json:"content"`
	Usage   *claudeUsage         `json:"usage,omitempty"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"` // "text", "tool_use", "tool_result"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// claudeResultEvent is a "result" event emitted when the CLI run completes.
type claudeResultEvent struct {
	Type       string       `json:"type"`
	Subtype    string       `json:"subtype"` // "success" or "error"
	IsError    bool         `json:"is_error"`
	Result     string       `json:"result"`
	StopReason string       `json:"stop_reason"`
	Usage      *claudeUsage `json:"usage,omitempty"`
	// ModelUsage contains per-model breakdowns; we extract aggregate usage instead.
}

// claudeErrorEvent is a top-level "error" event.
type claudeErrorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseClaudeStreamLine parses a single line of Claude Code stream-json output
// and returns zero or more StreamEvents. Unrecognized event types are silently skipped.
func parseClaudeStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	// Peek at the type field to decide which struct to unmarshal into.
	var envelope claudeEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude event: %w", err)
	}

	switch envelope.Type {
	case "assistant":
		return parseClaudeAssistant(line)
	case "result":
		return parseClaudeResult(line)
	case "error":
		return parseClaudeError(line)
	case "system", "rate_limit_event":
		// Informational — skip silently.
		return nil, nil
	default:
		// Unknown event type — skip.
		return nil, nil
	}
}

func parseClaudeAssistant(line []byte) ([]StreamEvent, error) {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse assistant event: %w", err)
	}

	var events []StreamEvent
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, StreamEvent{
					Type:    "delta",
					Content: block.Text,
				})
			}
		case "tool_use":
			input := make(map[string]any)
			if len(block.Input) > 0 {
				_ = json.Unmarshal(block.Input, &input)
			}
			events = append(events, StreamEvent{
				Type: "tool_use",
				ToolUse: &ToolUseBlock{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				},
			})
		// tool_result blocks are internal to Claude CLI's tool loop — skip.
		}
	}

	return events, nil
}

func parseClaudeResult(line []byte) ([]StreamEvent, error) {
	var ev claudeResultEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse result event: %w", err)
	}

	if ev.IsError || ev.Subtype == "error" {
		return []StreamEvent{
			{Type: "error", Error: ev.Result},
		}, nil
	}

	var events []StreamEvent

	// Emit usage if available.
	if ev.Usage != nil {
		stopReason := ev.StopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		events = append(events, StreamEvent{
			Type: "usage",
			Usage: &Usage{
				InputTokens:         ev.Usage.InputTokens,
				OutputTokens:        ev.Usage.OutputTokens,
				CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
				CacheReadTokens:     ev.Usage.CacheReadInputTokens,
				StopReason:          stopReason,
			},
		})
	}

	events = append(events, StreamEvent{Type: "done"})
	return events, nil
}

func parseClaudeError(line []byte) ([]StreamEvent, error) {
	var ev claudeErrorEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse error event: %w", err)
	}
	return []StreamEvent{
		{Type: "error", Error: ev.Error.Message},
	}, nil
}
