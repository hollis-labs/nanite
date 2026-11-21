package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/recovery"
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

// Input gives the user text, optional slots, a composed AgentDir profile and
// non-credential request Headers for this turn. Profile and headers are inherited
// by descendants and persisted for interrupted-turn recovery.
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

// EnsureConversation opens the conversation with this ID, creating it with
// the options on first use. Concurrent calls for the same ID on one Agent
// create it once; every caller gets the conversation, none gets an error
// because another created it first. Options apply only when it is created,
// and the ID always wins over an option that sets one.
func (a *Agent) EnsureConversation(ctx context.Context, id string, options ...ConversationOption) (*Conversation, error) {
	if id == "" {
		return nil, errors.New("nanite: EnsureConversation needs an ID")
	}
	if c, err := a.Conversation(ctx, id); !errors.Is(err, ErrNoConversation) {
		return c, err
	}
	a.ensureMu.Lock()
	defer a.ensureMu.Unlock()
	if c, err := a.Conversation(ctx, id); !errors.Is(err, ErrNoConversation) {
		return c, err
	}
	options = append(options[:len(options):len(options)], func(info *ConversationInfo) { info.ID = id })
	return a.NewConversation(ctx, options...)
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

// Resume continues an interrupted turn. It first validates the
// conversation's transcript and index, before reading its history; an unsafe
// finding refuses with an *IntegrityError (errors.Is ErrIntegrity) carrying
// the findings, and marks the conversation blocked, which refuses Send too
// until a check passes. Discard remains available.
func (c *Conversation) Resume(ctx context.Context) (*Run, error) {
	return c.agent.engine.Resume(ctx, c.id)
}

// CheckIntegrity validates the conversation's transcript and its index
// without writing anything, and returns every finding in order. Resume runs
// the same check and refuses when the report is not Safe.
func (c *Conversation) CheckIntegrity(ctx context.Context) (IntegrityReport, error) {
	return c.agent.engine.CheckIntegrity(ctx, c.id)
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

// History reads the persisted messages in order, excluding internal closing
// prompts. User messages and the agent's closing replies remain visible; stored
// sequence numbers are preserved and may have gaps.
func (c *Conversation) History(ctx context.Context) ([]Message, error) {
	history, err := c.agent.store.History(ctx, c.id)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(history, func(m Message) bool {
		return recovery.IsClosingPrompt(m.Role, m.Blocks)
	}), nil
}

// SourceNotice is the Input.Source of a turn a host starts because a notice
// arrived. Leave Input.Text empty: the engine sets the user message to
// NoticeTurnText, and the notice reaches the model in the session slot. Its
// trace record has source "reflex".
const SourceNotice = engine.SourceNotice

// NoticeTurnText is the user message of every notice turn.
const NoticeTurnText = engine.NoticeTurnText

// SourceAuto is the Input.Source of a turn the host sends on its own (a
// trigger, a reflex of the host's, another agent) rather than a person. Like
// a notice turn, it is not classified, runs no reflexes and does not reach a
// front desk; unlike one, it carries its own Input.Text. Its trace record
// has source "host", and its situation the source.
const SourceAuto = engine.SourceAuto

// SourceErrandPrefix starts the Input.Source of a turn one of the host's
// modules asked for; ErrandSource builds it.
const SourceErrandPrefix = engine.SourceErrandPrefix

// ErrandSource is the Input.Source of a turn the host's module asked for,
// "errand:<module>". It is steered like a SourceAuto turn. A module name is 1
// to 64 letters, digits, '.', '_' or '-', starting with a letter or digit.
func ErrandSource(module string) (string, error) { return engine.ErrandSource(module) }

// DispatchStart is one subtask a host starts with Dispatch, with the same
// fields as the model's subtask call. Variant is required; ReturnSpec is a
// JSON string (prose) or a JSON Schema object; Inputs and Context are JSON;
// Background starts it as a background job.
type DispatchStart struct {
	Goal       string          `json:"goal"`
	ReturnSpec json.RawMessage `json:"return_spec"`
	Variant    string          `json:"variant"`
	Inputs     json.RawMessage `json:"inputs,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
	Background bool            `json:"background,omitempty"`
}

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
	requests := make([]subagent.Request, len(starts))
	for i, s := range starts {
		requests[i] = subagent.Request{Goal: s.Goal, ReturnSpec: s.ReturnSpec, Variant: s.Variant, Inputs: s.Inputs, Context: s.Context, Background: s.Background}
	}
	return c.agent.engine.Dispatch(ctx, c.id, requests)
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

// SetMode sets the conversation's deliberate mode, a preset in
// Steering.Registry, for the person; "" clears it. A deliberate mode beats a
// classified one and is kept across restarts. Only a root conversation has a
// mode. This is the only way to set it: the model has no tool for it. It
// returns ErrNoSteering when the agent was opened without Steering.
func (c *Conversation) SetMode(ctx context.Context, name string) error {
	return c.agent.engine.SetMode(ctx, c.id, name)
}

// Mode is the conversation's deliberate mode, "" when none is set.
func (c *Conversation) Mode(ctx context.Context) (string, error) {
	return c.agent.engine.Mode(ctx, c.id)
}

// Steer runs steering for a person's message ahead of its turn, for a host
// that acts on the message before the turn (a front desk): if Halted, a
// reflex answered it, and nothing else should act on it. Send the turn with
// the result in Input.Steer, which then uses it instead of steering again;
// a reflex's cooldown starts here, so a Send that then fails has still used
// it. A PreparedSteer, or any copy of it, is used by one turn only. It returns ErrNoSteering when the agent
// was opened without Steering.
func (c *Conversation) Steer(ctx context.Context, in Input) (*PreparedSteer, error) {
	return c.agent.engine.Steer(ctx, c.id, in)
}

// SteerEvent runs the conversation's active event reflexes for a host event
// topic (a reflex on "a.b" fires for "a.b" and "a.b.c"), in the mode in
// force. What fired is returned for the host to carry out; a remind reflex's
// reminder waits for the next person's message. Nanite posts
// TopicCompacted itself after a compaction pass. Only a root conversation
// is steered; the model has no tool for this. It returns ErrNoSteering when
// the agent was opened without Steering.
func (c *Conversation) SteerEvent(ctx context.Context, topic string) (HostFired, error) {
	return c.agent.engine.SteerEvent(ctx, c.id, topic)
}

// SteerFocus runs the conversation's active focus reflexes for the view that
// came in front, as SteerEvent does for an event.
func (c *Conversation) SteerFocus(ctx context.Context, view string) (HostFired, error) {
	return c.agent.engine.SteerFocus(ctx, c.id, view)
}

// AcceptSuggestion applies a classifier suggestion (from a Steered event)
// the person accepted: a preset becomes the deliberate mode, a work slot is
// pinned until the window in front changes. view is the view in front.
func (c *Conversation) AcceptSuggestion(ctx context.Context, suggestion SteerVerdict, view string) error {
	return c.agent.engine.AcceptSuggestion(ctx, c.id, suggestion, view)
}

// TurnContext validates a turn's profile and headers and returns a context
// carrying their snapshot. Hosts that plan or Dispatch before Send use this
// context for all three operations so background work keeps the same profile.
// The context is scoped to this agent; it must not be reused for another agent.
func (a *Agent) TurnContext(ctx context.Context, in Input) (context.Context, error) {
	return a.engine.TurnContext(ctx, in)
}
