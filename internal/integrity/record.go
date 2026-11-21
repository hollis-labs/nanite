package integrity

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Sideband subtypes this version knows. The store writes them; see
// internal/store/journal.go.
const (
	subtypeConversation = "nanite.conversation"
	subtypeTurnStart    = "nanite.turn.start"
	subtypeTurnFinish   = "nanite.turn.finish"
	subtypeTurnInt      = "nanite.turn.interrupt"
	subtypeTurnResume   = "nanite.turn.resume"
	subtypeCompaction   = "nanite.compaction"
	subtypePermission   = "nanite.permission"
)

// Turn statuses. A finish record carries one of the terminal ones.
const (
	statusRunning     = "running"
	statusInterrupted = "interrupted"
	statusCompleted   = "completed"
	statusFailed      = "failed"
	statusCancelled   = "cancelled"
)

func terminal(status string) bool {
	return status == statusCompleted || status == statusFailed || status == statusCancelled
}

// record is the part of a transcript line the checks read. It decodes the
// way the store's projection does, timestamp included, so a record the
// projection would refuse is refused here too.
type record struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	UUID      string          `json:"uuid"`
	SessionID string          `json:"sessionId"`
	Timestamp time.Time       `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Payload   json.RawMessage `json:"payload"`
}

type messageBody struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	TurnID  string          `json:"nanite_turn_id"`
}

// block mirrors llmtypes.ContentBlock field for field. History decodes
// every stored block into that type, so a field of the wrong type there makes
// the conversation unreadable even though the index projection accepted it.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     *map[string]any `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   string          `json:"content"`
	IsError   bool            `json:"is_error"`
	Signature string          `json:"signature"`
}

// The payloads mirror the store's Turn, Compaction and Conversation field
// for field: the projection decodes into those types and fails the
// conversation on a field of the wrong type, so these must too.
type turnPayload struct {
	AgentDir                                     string
	Headers                                      map[string]string
	ID, UserMessageID, Status, StopReason, Error string
	Usage                                        json.RawMessage
}

type compactionPayload struct {
	TurnID, ThroughMessageID, Summary string
	Before, After                     int
}

type conversationPayload struct {
	ID, ParentID, Role, Title, Status string
	CreatedAt, UpdatedAt              time.Time
}

var errShape = errors.New("record shape")

// message is a decoded user or assistant record.
type message struct {
	id     string
	role   string // user or assistant, as written
	turn   string
	first  string // the first block's type, which the index role depends on
	blocks []block
}

// kind classifies a message the way a turn writes it.
type kind int

const (
	kindOwner     kind = iota // the person's message that opens a turn
	kindAssistant             // a model step
	kindTool                  // tool results, stored with the user role
	kindClosing               // a closing-reply host note, stored with the user role
)

func (m message) kind() kind {
	if m.role == "assistant" {
		return kindAssistant
	}
	for _, b := range m.blocks {
		if b.Type == "tool_result" {
			return kindTool
		}
	}
	for _, b := range m.blocks {
		if b.Type == "text" && strings.HasPrefix(b.Name, ClosingPromptPrefix) {
			return kindClosing
		}
	}
	return kindOwner
}

// indexRole is the role the index projection stores: it calls a user message
// whose first block is a tool result "tool". This mirrors projectMessage.
func (m message) indexRole() string {
	if m.role == "user" && m.first == "tool_result" {
		return "tool"
	}
	return m.role
}

// projected decodes a message exactly as projectMessage does: the body, then
// the content as an array of blocks with a string type and text. It is what
// Project relies on, so Project agrees with the index row for row.
func projected(r record) (message, error) {
	var body messageBody
	if err := json.Unmarshal(r.Message, &body); err != nil {
		return message{}, err
	}
	var blocks []struct{ Type, Text string }
	if err := json.Unmarshal(body.Content, &blocks); err != nil {
		return message{}, err
	}
	m := message{id: r.UUID, role: body.Role, turn: body.TurnID}
	if len(blocks) > 0 {
		m.first = blocks[0].Type
	}
	return m, nil
}

// decodeMessage reads a message the way both the projection and History
// will: it fails where either would. The role and UUID are checked by
// Validate.
func decodeMessage(r record) (message, error) {
	m, err := projected(r)
	if err != nil {
		return message{}, err
	}
	var body messageBody
	if err := json.Unmarshal(r.Message, &body); err != nil {
		return message{}, err
	}
	if err := json.Unmarshal(body.Content, &m.blocks); err != nil {
		return message{}, err
	}
	return m, nil
}

func decodePayload[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, errShape
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}
