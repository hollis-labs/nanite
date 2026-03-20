package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
)

// StructuredMessage is the universal wrapper for all assistant messages.
type StructuredMessage struct {
	Version   int            `json:"v"`
	Text      string         `json:"text"`
	Tier      string         `json:"tier"`
	Hash      string         `json:"hash,omitempty"`
	Envelopes []EnvelopeRef  `json:"envelopes,omitempty"`
	ToolCalls []ToolCallRef  `json:"tool_calls,omitempty"`
	Flags     MessageFlags   `json:"flags"`
}

// MessageFlags contains boolean flags for message state.
type MessageFlags struct {
	Truncated   bool `json:"truncated,omitempty"`
	HasError    bool `json:"has_error,omitempty"`
	Provisional bool `json:"provisional,omitempty"`
}

// ToolCallRef records a tool call made during the response.
type ToolCallRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	HasEnvelope bool   `json:"has_envelope,omitempty"`
}

// EnvelopeRef is a typed reference to an envelope embedded in the message.
type EnvelopeRef struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WrapResponse creates a StructuredMessage wrapping the assistant's response text.
func WrapResponse(text, tier string, toolCalls []ToolCallRef, envelopes []EnvelopeRef, truncated, hasError bool) StructuredMessage {
	msg := StructuredMessage{
		Version:   1,
		Text:      text,
		Tier:      tier,
		Envelopes: envelopes,
		ToolCalls: toolCalls,
		Flags: MessageFlags{
			Truncated: truncated,
			HasError:  hasError,
		},
	}
	// Compute hash of final text.
	if text != "" {
		msg.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	}
	return msg
}

// MarshalContent returns the JSON string for saving to Message.Content.
func (sm StructuredMessage) MarshalContent() string {
	data, err := json.Marshal(sm)
	if err != nil {
		return sm.Text // fallback to raw text
	}
	return string(data)
}

// ValidateStructured checks a StructuredMessage for consistency and returns warnings.
func ValidateStructured(sm StructuredMessage) []string {
	var warnings []string
	if sm.Version != 1 {
		warnings = append(warnings, fmt.Sprintf("unknown version: %d", sm.Version))
	}
	if sm.Tier != "default" && sm.Tier != "tool" {
		warnings = append(warnings, fmt.Sprintf("unknown tier: %s", sm.Tier))
	}
	if sm.Hash != "" && sm.Text != "" {
		expected := fmt.Sprintf("%x", sha256.Sum256([]byte(sm.Text)))
		if sm.Hash != expected {
			warnings = append(warnings, "hash mismatch — content may be corrupted")
		}
	}
	return warnings
}

// logStructuredWarnings validates a StructuredMessage and logs any warnings.
func logStructuredWarnings(sm StructuredMessage) {
	if warnings := ValidateStructured(sm); len(warnings) > 0 {
		for _, w := range warnings {
			log.Printf("chat: structured message warning: %s", w)
		}
	}
}
