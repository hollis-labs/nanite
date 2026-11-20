package agent

import (
	"context"
	"time"

	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// ConversationInfo identifies a stored chat or subagent, its optional
// parent, its current status, and its creation and update times.
type ConversationInfo = store.Conversation

// ConversationOption configures a new conversation.
type ConversationOption func(*ConversationInfo)

// Conversation is one thread of model and tool messages.
type Conversation struct {
	id    string
	agent *Agent
}

// Input gives the user text and optional slot entries applied only to this
// turn. The entries are discarded when the turn ends.
type Input = engine.Input

// Message is a timestamped transcript entry. Role identifies the author,
// TurnID links it to a turn, and Blocks retain the original content order.
type Message = store.Message

// InterruptedTurn identifies the unfinished turn and its stored user message.
type InterruptedTurn = store.InterruptedTurn

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
func (c *Conversation) Interrupted() *InterruptedTurn {
	turn, _ := c.agent.store.Interrupted(context.Background(), c.id)
	return turn
}

// Resume continues an interrupted turn.
func (c *Conversation) Resume(ctx context.Context) (*Run, error) {
	return c.agent.engine.Resume(ctx, c.id)
}

// Discard abandons an interrupted turn instead of resuming it. The
// conversation keeps what the turn already stored and accepts new messages.
func (c *Conversation) Discard(ctx context.Context) error {
	return c.agent.engine.Discard(ctx, c.id)
}

// Record adds an exchange the host answered itself, without calling the
// model, so the agent's history includes it on the next turn. It fails while
// a turn is running or interrupted.
func (c *Conversation) Record(ctx context.Context, userText, answer string) error {
	return c.agent.engine.Record(ctx, c.id, userText, answer)
}

// History reads the persisted messages in order.
func (c *Conversation) History(ctx context.Context) ([]Message, error) {
	return c.agent.store.History(ctx, c.id)
}

// SourceNotice is the Input.Source of a turn a host starts because a notice
// arrived. Its trace record has source "reflex".
const SourceNotice = engine.SourceNotice

// DispatchStart is one subtask a host starts with Dispatch: the same fields
// as the model's subtask call (goal, return_spec, variant, inputs, context,
// background). The variant is required.
type DispatchStart = subagent.Request

// ErrNoStarts is returned by Dispatch when it is given nothing to start.
var ErrNoStarts = engine.ErrNoStarts

// Dispatch starts subtasks for the host, without a model call. They take the
// same path as a model's subtask call: variants and their budgets, the
// subtask slot limits, the agent's per-turn token budget, forked context
// (the conversation's latest settled messages), the result contract, relayed
// approvals (attributed as dispatched) and progress events.
//
// Dispatch holds the conversation like a turn until every foreground start
// has ended; it fails with ErrBusy while a turn runs. Read the returned run's
// Events as for a turn (subtask lifecycle, progress, relayed approvals), then
// Wait: Result.Dispatched has one outcome per start, in order. A background
// start's outcome is its job handle; it posts a notice when it finishes, and
// Job reads its result. Every start is checked before anything runs. The
// conversation must be a root chat; its history does not change.
func (c *Conversation) Dispatch(ctx context.Context, starts []DispatchStart) (*Run, error) {
	return c.agent.engine.Dispatch(ctx, c.id, starts)
}

// PostNotice posts a notice to the conversation: it is announced at once (on
// the running turn, if any, and to every notice subscriber) and waits in the
// store for the next turn, which delivers it in the session slot.
func (c *Conversation) PostNotice(ctx context.Context, kind, body string) error {
	return c.agent.engine.PostNotice(ctx, c.id, kind, body)
}

// Job reads one of the conversation's background jobs (a dispatched or
// model-started background subtask, or a shell command) as the
// background_status tool reports it: JSON with id, kind, status and, for a
// finished subtask, its result.
func (c *Conversation) Job(ctx context.Context, id string) (string, error) {
	return c.agent.engine.Job(ctx, c.id, id)
}
