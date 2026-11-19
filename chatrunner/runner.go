package chatrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/nanite/agent"
)

// Options configures a Runner.
type Options struct {
	// Agent opens the Nanite agent. Dir is required. Gate is set by the
	// runner and must be left nil; PDP, when set, is consulted before the
	// runner's own checks. Builtins chooses Nanite's own tool groups as usual
	// (none by default for an embedded agent).
	Agent agent.Options
	// Tools are the host's tools. Replace them later with SetTools.
	Tools []Tool
	// Ask is called before each host-tool call. Returning true makes that
	// call ask first, whatever the tool's level. It can only make a call
	// stricter.
	Ask func(ctx context.Context, call Call) bool
	// Granted reports whether the host holds a standing grant that covers a
	// call (one it stored through chat.Grants). A covered call runs without
	// asking. It is not consulted for a LevelConfirm tool.
	Granted func(ctx context.Context, call Call) bool
	// ContextSlot is the slot the turn's context is placed in. Default
	// agent.SlotUserContext.
	ContextSlot agent.SlotName
}

// Runner is a chat.Runner backed by one Nanite agent.
type Runner struct {
	agent    *agent.Agent
	opts     Options
	replaced chan string

	mu     sync.Mutex
	tools  map[string]Tool
	active map[string]*run // by Nanite conversation ID
}

var _ chat.Runner = (*Runner)(nil)

// Open opens the agent directory and returns a runner for it. Close it when
// the host shuts down.
func Open(ctx context.Context, opts Options) (*Runner, error) {
	if opts.Agent.Gate != nil {
		return nil, errors.New("chatrunner: Options.Agent.Gate must be nil; approvals are answered on the chat wire")
	}
	if opts.ContextSlot == "" {
		opts.ContextSlot = agent.SlotUserContext
	}
	r := &Runner{opts: opts, replaced: make(chan string), active: map[string]*run{}}
	ao := opts.Agent
	ao.Gate = gate{r}
	ao.PDP = pdp{runner: r, host: opts.Agent.PDP}
	a, err := agent.Open(ctx, ao)
	if err != nil {
		return nil, err
	}
	r.agent = a
	if err := r.SetTools(opts.Tools...); err != nil {
		_ = a.Close()
		return nil, err
	}
	return r, nil
}

// Agent returns the Nanite agent, for its slots, skills and notices. Use
// SetTools for the host's tools so their levels are kept.
func (r *Runner) Agent() *agent.Agent { return r.agent }

// Close stops running turns and releases the agent directory.
func (r *Runner) Close() error { return r.agent.Close() }

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ConversationID is the Nanite conversation that serves a chat conversation.
// It is the RunnerRef on the chat conversation record.
func ConversationID(chatID string) string {
	if safeID.MatchString(chatID) {
		return "chat-" + chatID
	}
	sum := sha256.Sum256([]byte(chatID))
	return "chat-" + hex.EncodeToString(sum[:16])
}

// conversation opens the Nanite conversation for a chat conversation,
// creating it on first use. A turn the previous process left unfinished is
// discarded: the chat Service has already ended that run on the stream.
func (r *Runner) conversation(ctx context.Context, chatID string) (*agent.Conversation, error) {
	id := ConversationID(chatID)
	c, err := r.agent.Conversation(ctx, id)
	if errors.Is(err, agent.ErrNoConversation) {
		c, err = r.agent.NewConversation(ctx, func(info *agent.ConversationInfo) { info.ID = id })
	}
	if err != nil {
		return nil, fmt.Errorf("chatrunner: open conversation: %w", err)
	}
	if c.Interrupted() != nil {
		if err := c.Discard(ctx); err != nil {
			return nil, fmt.Errorf("chatrunner: discard interrupted turn: %w", err)
		}
	}
	return c, nil
}

type turnKey struct{}

// TurnFromContext returns the chat turn a host tool is running for.
func TurnFromContext(ctx context.Context) (chat.Turn, bool) {
	t, ok := ctx.Value(turnKey{}).(chat.Turn)
	return t, ok
}

// Start begins a turn.
func (r *Runner) Start(ctx context.Context, turn chat.Turn) (chat.Run, error) {
	c, err := r.conversation(ctx, turn.ConversationID)
	if err != nil {
		return nil, err
	}
	in := agent.Input{Text: turn.Text}
	if text := contextText(turn.Context); text != "" {
		in.Slots = []agent.SlotUpdate{{Slot: r.opts.ContextSlot, Entry: agent.SlotEntry{Owner: "chat", Key: "turn-context", Content: text}}}
	}
	out := &run{runner: r, turn: turn, nanite: c.ID(), pending: map[string]*pending{}, asked: map[string]bool{}}
	if info, err := r.agent.Info(ctx); err == nil {
		out.provider, out.model = info.Definition.Profile.Runtime.Provider, info.Definition.Profile.Runtime.Model
	}
	// The turn outlives the call that started it, and is stopped with Cancel.
	runCtx, cancel := context.WithCancel(context.WithValue(context.WithoutCancel(ctx), turnKey{}, turn))
	out.ctx, out.stop = runCtx, cancel

	r.mu.Lock()
	r.active[out.nanite] = out
	r.mu.Unlock()
	inner, err := c.Send(runCtx, in)
	if err != nil {
		cancel()
		r.release(out)
		return nil, fmt.Errorf("chatrunner: %w", err)
	}
	out.inner = inner
	return out, nil
}

// contextText is the turn's context as the text placed in the slot: a JSON
// string's value, or any other JSON as written.
func contextText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return trimmed
}

// Info reports what the runner does and the Nanite conversation that serves
// a chat conversation.
func (r *Runner) Info(_ context.Context, conversationID string) (chat.Info, error) {
	return chat.Info{Deltas: true, Approvals: true, RunnerRef: ConversationID(conversationID)}, nil
}

// Record adds an exchange the host answered itself to the agent's history.
func (r *Runner) Record(ctx context.Context, turn chat.Turn, answer string) error {
	if strings.TrimSpace(turn.Text) == "" || strings.TrimSpace(answer) == "" {
		return nil
	}
	c, err := r.conversation(ctx, turn.ConversationID)
	if err != nil {
		return err
	}
	return c.Record(ctx, turn.Text, answer)
}

// Replaced never sends: a Nanite conversation is not replaced behind the
// Service's back.
func (r *Runner) Replaced() <-chan string { return r.replaced }

func (r *Runner) activeRun(naniteID string) *run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[naniteID]
}

func (r *Runner) release(x *run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[x.nanite] == x {
		delete(r.active, x.nanite)
	}
}

// chatID is the chat conversation a Nanite conversation is serving right
// now, or empty when no turn is running in it.
func (r *Runner) chatID(naniteID string) string {
	if x := r.activeRun(naniteID); x != nil {
		return x.turn.ConversationID
	}
	return ""
}
