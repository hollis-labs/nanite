package agent

import (
	"context"
	"time"

	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/store"
)

// ConversationInfo describes a persisted conversation.
type ConversationInfo = store.Conversation

// ConversationOption configures a new conversation.
type ConversationOption func(*ConversationInfo)

// Conversation is one thread of model and tool messages.
type Conversation struct {
	id    string
	agent *Agent
}

// Input supplies a user message and turn-scoped slot updates.
type Input = engine.Input

// Message is one persisted user, assistant or tool message.
type Message = store.Message

// InterruptedTurn identifies a turn that can be resumed.
type InterruptedTurn struct{ ID, UserMessageID string }

// NewConversation creates a conversation.
func (a *Agent) NewConversation(ctx context.Context, options ...ConversationOption) (*Conversation, error) {
	now := time.Now().UTC()
	info := ConversationInfo{ID: store.NewID(now), Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}
	for _, apply := range options {
		apply(&info)
	}
	if err := a.store.CreateConversation(ctx, info); err != nil {
		return nil, err
	}
	return &Conversation{id: info.ID, agent: a}, nil
}

// Conversation opens an existing conversation by ID.
func (a *Agent) Conversation(ctx context.Context, id string) (*Conversation, error) {
	if _, err := a.store.Conversation(ctx, id); err != nil {
		return nil, err
	}
	return &Conversation{id: id, agent: a}, nil
}

// Latest opens the most recently updated conversation.
func (a *Agent) Latest(ctx context.Context) (*Conversation, error) {
	info, err := a.store.Latest(ctx)
	if err != nil {
		return nil, err
	}
	return &Conversation{id: info.ID, agent: a}, nil
}

// Conversations lists existing conversations.
func (a *Agent) Conversations(ctx context.Context) ([]ConversationInfo, error) {
	return a.store.Conversations(ctx)
}

// ID returns the conversation ID.
func (c *Conversation) ID() string { return c.id }

// Send begins a turn.
func (c *Conversation) Send(ctx context.Context, in Input) (*Run, error) {
	return c.agent.engine.Send(ctx, c.id, in)
}

// Interrupted reports the turn left unfinished by the previous process.
func (c *Conversation) Interrupted() *InterruptedTurn { return nil }

// Resume continues an interrupted turn.
func (c *Conversation) Resume(context.Context) (*Run, error) { return nil, ErrNotImplemented }

// History reads the persisted messages in order.
func (c *Conversation) History(ctx context.Context) ([]Message, error) {
	return c.agent.store.History(ctx, c.id)
}
