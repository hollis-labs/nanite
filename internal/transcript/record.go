package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
)

// MessageRecord is the Claude Code transcript envelope. Message contains a
// Claude-shaped message object with role and content blocks. UUID, ParentUUID
// and SessionID come from the host; none is minted by this package.
type MessageRecord struct {
	Type       string                     `json:"type"`
	UUID       string                     `json:"uuid"`
	ParentUUID *string                    `json:"parentUuid"`
	SessionID  string                     `json:"sessionId"`
	Timestamp  time.Time                  `json:"timestamp"`
	Message    json.RawMessage            `json:"message"`
	ResultRef  *runtimecontract.Reference `json:"resultRef,omitempty"`
}

// EncodeMessage checks the native wrapper and preserves the message object.
// Usage, thinking, tool_use and tool_result blocks remain in Message without
// lossy conversion. The host must supply scrubbed content as appropriate.
func EncodeMessage(m MessageRecord) (json.RawMessage, error) {
	if m.Type != "user" && m.Type != "assistant" {
		return nil, ErrInvalid
	}
	if m.UUID == "" || m.SessionID == "" || m.Timestamp.IsZero() || len(m.Message) == 0 {
		return nil, ErrInvalid
	}
	var body struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(m.Message, &body); err != nil || body.Role != m.Type || len(body.Content) == 0 {
		return nil, fmt.Errorf("%w: message role/content", ErrInvalid)
	}
	content := bytes.TrimSpace(body.Content)
	if len(content) == 0 || (content[0] != '[' && content[0] != '"') {
		return nil, fmt.Errorf("%w: message content", ErrInvalid)
	}
	if m.ResultRef != nil {
		if m.Type != "user" || m.ResultRef.Validate() != nil {
			return nil, fmt.Errorf("%w: result reference", ErrInvalid)
		}
		var blocks []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		}
		if err := json.Unmarshal(content, &blocks); err != nil {
			return nil, fmt.Errorf("%w: tool result blocks", ErrInvalid)
		}
		found := false
		for _, block := range blocks {
			if block.Type == "tool_result" && block.ToolUseID != "" {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: reference requires tool_result", ErrInvalid)
		}
	}
	return json.Marshal(m)
}

// SidebandRecord is a Claude system or summary row. Subtypes distinguish host
// approval, outcome, usage and compaction records. Payload is kept verbatim in
// an object for host readers; ordinary Claude transcript readers can skip an
// unfamiliar subtype while retaining user/assistant message compatibility.
type SidebandRecord struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype,omitempty"`
	UUID       string          `json:"uuid"`
	ParentUUID *string         `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  time.Time       `json:"timestamp"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func EncodeSideband(s SidebandRecord) (json.RawMessage, error) {
	if s.Type != "system" && s.Type != "summary" {
		return nil, ErrInvalid
	}
	if s.UUID == "" || s.SessionID == "" || s.Timestamp.IsZero() || s.Subtype == "" {
		return nil, ErrInvalid
	}
	if len(s.Payload) > 0 {
		payload := bytes.TrimSpace(s.Payload)
		if len(payload) == 0 || !json.Valid(payload) || payload[0] != '{' {
			return nil, ErrInvalid
		}
	}
	return json.Marshal(s)
}
