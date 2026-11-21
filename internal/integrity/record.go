package integrity

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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

// record is the part of a transcript line the checks read.
type record struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	UUID      string          `json:"uuid"`
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
	Payload   json.RawMessage `json:"payload"`
}

type messageBody struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	TurnID  string          `json:"nanite_turn_id"`
}

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

// turnPayload is the store's Turn as a turn sideband carries it.
type turnPayload struct {
	ID, UserMessageID, Status string
}

type compactionPayload struct {
	TurnID, ThroughMessageID, Summary string
}

type conversationPayload struct{ ID string }

var errShape = errors.New("record shape")

// message is a decoded user or assistant record.
type message struct {
	id     string
	role   string // user or assistant, as written
	turn   string
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
	if m.role == "user" && len(m.blocks) > 0 && m.blocks[0].Type == "tool_result" {
		return "tool"
	}
	return m.role
}

func decodeMessage(r record) (message, error) {
	var body messageBody
	if err := json.Unmarshal(r.Message, &body); err != nil {
		return message{}, err
	}
	// The role and UUID are checked by Validate, not here: the projection
	// stores what the body says, and Project must agree with it.
	m := message{id: r.UUID, role: body.Role, turn: body.TurnID}
	// Claude-shape readers accept string content, but the index projection
	// reads a block array and fails the whole conversation on anything else.
	content := bytes.TrimSpace(body.Content)
	if len(content) == 0 || content[0] != '[' {
		return message{}, errShape
	}
	if err := json.Unmarshal(content, &m.blocks); err != nil {
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
