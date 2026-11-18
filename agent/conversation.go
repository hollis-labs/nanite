package agent

import (
	"context"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// ConversationInfo describes a persisted conversation.
type ConversationInfo struct {
	ID, ParentID, Role, Title, Status string
	CreatedAt, UpdatedAt              time.Time
}

// ConversationOption configures a new conversation.
type ConversationOption func(*ConversationInfo)

// Conversation is one thread of model and tool messages.
type Conversation struct{ id string }

// Input supplies a user message and turn-scoped slot updates.
type Input struct {
	Text  string
	Slots []SlotUpdate
}

// Message is one persisted user, assistant or tool message.
type Message struct {
	ID, TurnID, Role string
	Blocks           []llmtypes.ContentBlock
	CreatedAt        time.Time
}

// InterruptedTurn identifies a turn that can be resumed.
type InterruptedTurn struct{ ID, UserMessageID string }

// NewConversation creates a conversation.
func (a *Agent) NewConversation(context.Context, ...ConversationOption) (*Conversation, error) {
	return nil, ErrNotImplemented
}

// Conversation opens an existing conversation by ID.
func (a *Agent) Conversation(context.Context, string) (*Conversation, error) {
	return nil, ErrNotImplemented
}

// Latest opens the most recently updated conversation.
func (a *Agent) Latest(context.Context) (*Conversation, error) { return nil, ErrNoConversation }

// Conversations lists existing conversations.
func (a *Agent) Conversations(context.Context) ([]ConversationInfo, error) {
	return nil, ErrNotImplemented
}

// ID returns the conversation ID.
func (c *Conversation) ID() string { return c.id }

// Send begins a turn.
func (c *Conversation) Send(context.Context, Input) (*Run, error) { return nil, ErrNotImplemented }

// Interrupted reports the turn left unfinished by the previous process.
func (c *Conversation) Interrupted() *InterruptedTurn { return nil }

// Resume continues an interrupted turn.
func (c *Conversation) Resume(context.Context) (*Run, error) { return nil, ErrNotImplemented }

// History reads the persisted messages in order.
func (c *Conversation) History(context.Context) ([]Message, error) { return nil, ErrNotImplemented }
