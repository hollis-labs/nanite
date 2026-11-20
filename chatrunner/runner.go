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
	// Observe publishes a run the runner began itself on a chat
	// conversation; set it to the chat Service's Observe. With it, a notice
	// posted while no turn runs (a background subtask finishing, a relayed
	// ask waiting) reaches the wire as a nanite.notice activity, and Dispatch
	// can publish a host dispatch. Without it, such notices wait for the
	// conversation's next turn.
	Observe func(ctx context.Context, conversationID string, turn chat.Turn, run chat.Run) error
	// NoticeTurns makes a notice posted while no turn runs start a notice
	// turn (agent.SourceNotice; its user message is agent.NoticeTurnText and
	// the notice reaches the model in the session slot), instead of only
	// announcing it. A chained notice (caused by work a notice turn started)
	// never starts one, and at most three notice turns follow one another
	// before a user turn. The turn starts only once the Service adopts it.
	// It needs Observe. Off by default.
	NoticeTurns bool
}

// Runner is a chat.Runner backed by one Nanite agent.
type Runner struct {
	agent    *agent.Agent
	opts     Options
	replaced chan string

	mu     sync.Mutex
	tools  map[string]Tool
	active map[string]*run // by Nanite conversation ID
	// chats maps a Nanite conversation to the chat conversation it serves,
	// kept after its turn ends: a background subtask's ask may be decided
	// between turns.
	chats map[string]string
	// stopNotices ends the notice listener; noticesDone is closed when it
	// has stopped.
	stopNotices context.CancelFunc
	noticesDone chan struct{}
	// noticeTurns counts notice turns in a row per Nanite conversation;
	// a user turn resets it.
	noticeTurns map[string]int
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
	r := &Runner{opts: opts, replaced: make(chan string), active: map[string]*run{}, chats: map[string]string{}, noticeTurns: map[string]int{}}
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
	listen, stop := context.WithCancel(context.WithoutCancel(ctx))
	r.stopNotices, r.noticesDone = stop, make(chan struct{})
	notices := a.SubscribeNotices(listen)
	go r.listen(listen, notices)
	return r, nil
}

// Agent returns the Nanite agent, for its slots, skills and notices. Use
// SetTools for the host's tools so their levels are kept.
func (r *Runner) Agent() *agent.Agent { return r.agent }

// Close stops running turns and releases the agent directory.
func (r *Runner) Close() error {
	r.stopNotices()
	<-r.noticesDone
	return r.agent.Close()
}

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
	r.mu.Lock()
	r.chats[id] = chatID
	r.mu.Unlock()
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
	in := agent.Input{Text: turn.Text}
	if text := contextText(turn.Context); text != "" {
		in.Slots = []agent.SlotUpdate{{Slot: r.opts.ContextSlot, Entry: agent.SlotEntry{Owner: "chat", Key: "turn-context", Content: text}}}
	}
	x, err := r.begin(ctx, turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) { return c.Send(runCtx, in) })
	if err != nil {
		return nil, err
	}
	// A user turn lets notices start notice turns again.
	r.mu.Lock()
	delete(r.noticeTurns, x.nanite)
	r.mu.Unlock()
	return x, nil
}

// begin opens the chat conversation's Nanite conversation and starts a run
// on it with start, registered so its approvals are answered on the wire.
func (r *Runner) begin(ctx context.Context, turn chat.Turn, start func(*agent.Conversation, context.Context) (*agent.Run, error)) (*run, error) {
	c, err := r.conversation(ctx, turn.ConversationID)
	if err != nil {
		return nil, err
	}
	out := &run{runner: r, turn: turn, nanite: c.ID(), pending: map[string]*pending{}, asked: map[string]bool{}, ended: make(chan struct{})}
	if info, err := r.agent.Info(ctx); err == nil {
		out.provider, out.model = info.Definition.Profile.Runtime.Provider, info.Definition.Profile.Runtime.Model
	}
	// The run outlives the call that started it, and is stopped with Cancel.
	runCtx, cancel := context.WithCancel(context.WithValue(context.WithoutCancel(ctx), turnKey{}, turn))
	out.ctx, out.stop = runCtx, cancel

	r.mu.Lock()
	if r.active[out.nanite] != nil {
		r.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("chatrunner: %w", agent.ErrBusy)
	}
	r.active[out.nanite] = out
	r.mu.Unlock()
	inner, err := start(c, runCtx)
	if err != nil {
		cancel()
		r.release(out)
		return nil, fmt.Errorf("chatrunner: %w", err)
	}
	r.mu.Lock()
	out.inner = inner
	r.mu.Unlock()
	return out, nil
}

// Dispatch starts subtasks for the host on a chat conversation, without a
// model call (see agent.Conversation.Dispatch), and publishes the dispatch
// as a run on the wire through Options.Observe: its subtask activity and any
// approvals its subtasks relay, which are answered on the wire like a turn's.
// It returns once every foreground start has ended, with one outcome per
// start; a background start's outcome is its job handle, and its finish is
// announced as a nanite.notice. It needs Options.Observe.
func (r *Runner) Dispatch(ctx context.Context, conversationID string, starts []agent.DispatchStart) ([]agent.Dispatched, error) {
	if r.opts.Observe == nil {
		return nil, errors.New("chatrunner: Dispatch needs Options.Observe")
	}
	turn := chat.Turn{ConversationID: conversationID}
	x, err := r.begin(ctx, turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
		return c.Dispatch(runCtx, starts)
	})
	if err != nil {
		return nil, err
	}
	// A dispatch makes no model request: its run.start names no model.
	x.provider, x.model = "", ""
	if err := r.opts.Observe(ctx, conversationID, turn, x); err != nil {
		// Nobody will read the run; stop it and let it finish.
		x.Cancel()
		_, _ = x.inner.Wait()
		r.release(x)
		return nil, fmt.Errorf("chatrunner: publish dispatch: %w", err)
	}
	select {
	case <-x.ended:
	case <-ctx.Done():
		x.Cancel()
		<-x.ended
	}
	result, err := x.inner.Wait()
	return result.Dispatched, err
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

// activeRunID is the Nanite run ID of the runner's running turn on a
// conversation, or empty.
func (r *Runner) activeRunID(naniteID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if x := r.active[naniteID]; x != nil && x.inner != nil {
		return x.inner.ID()
	}
	return ""
}

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

// chatID is the chat conversation a Nanite conversation serves: the running
// turn's, or else the one it last served, or empty if it never served one.
func (r *Runner) chatID(naniteID string) string {
	if x := r.activeRun(naniteID); x != nil {
		return x.turn.ConversationID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chats[naniteID]
}
