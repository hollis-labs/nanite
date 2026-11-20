package chatrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/steer"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
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
	// Desk turns on the front desk: intake planning on each user message,
	// plans dispatched as background subtasks and tracked by topic, and a
	// delivery editor that paces the notices no running turn carried into
	// composed messages. Nil leaves notices published one by one as they
	// arrive. It needs Observe.
	Desk *DeskOptions
	// Situation, with Agent.Steering, says what is in front of the person
	// for a turn (the window in front, the open windows, the layout), for
	// steering. Nil steers with no situation.
	Situation func(turn chat.Turn) steer.Situation
	// OnSteered, with Agent.Steering, sees what steering decided for each
	// root turn, including the reflex actions for the host to carry out.
	// Those actions never go on the wire; the nanite.steer activity carries
	// the rest. It runs inline with the turn's wire events: keep it fast,
	// and hand slow work off.
	OnSteered func(chatID string, s agent.Steered)
	// ResumeInterrupted makes RecoverTurns resume each turn the previous
	// process left unfinished. Without it, RecoverTurns offers each one to
	// the owner (a nanite.interrupted activity) to Resume or Discard.
	ResumeInterrupted bool
	// InterruptedHold is how long an interrupted turn waits for the owner
	// (or RecoverTurns) before the runner discards it (default 15 minutes;
	// negative waits indefinitely). The front desk's work on that chat
	// waits meanwhile. The clock survives a restart.
	InterruptedHold time.Duration
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
	// publishers publishes each conversation's notices in order;
	// publishing counts the running ones.
	publishers map[string]*publisher
	publishing sync.WaitGroup
	noticeCtx  context.Context
	closed     bool
	desk       *Desk
	// recorded is the Nanite conversations whose chat is saved as host
	// state in this process.
	recorded map[string]bool
	// holds is the interrupted turns waiting for the owner, by chat;
	// resuming is the chats whose turn a Resume has claimed.
	holds    map[string]*hold
	resuming map[string]bool
	// serving is set once the chat Service is known to run.
	serving bool
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
	if opts.Desk != nil && opts.Observe == nil {
		return nil, errors.New("chatrunner: Options.Desk needs Options.Observe")
	}
	r := &Runner{opts: opts, replaced: make(chan string), active: map[string]*run{}, chats: map[string]string{}, noticeTurns: map[string]int{}, publishers: map[string]*publisher{},
		recorded: map[string]bool{}, holds: map[string]*hold{}, resuming: map[string]bool{}}
	if opts.Desk != nil {
		desk := *opts.Desk
		if desk.Mode == nil && opts.Agent.Steering != nil {
			desk.Mode = r.mode
		}
		d, err := newDesk(r, desk)
		if err != nil {
			return nil, err
		}
		r.desk = d
	}
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
	if r.desk != nil {
		// The desk's saved state is back before any notice arrives.
		if err := r.desk.load(ctx); err != nil {
			_ = a.Close()
			return nil, err
		}
	}
	listen, stop := context.WithCancel(context.WithoutCancel(ctx))
	r.stopNotices, r.noticesDone, r.noticeCtx = stop, make(chan struct{}), listen
	notices := a.SubscribeNotices(listen)
	go r.listen(listen, notices)
	if r.desk != nil {
		// A failure here leaves the step for the next turn's advance.
		if err := r.desk.Recover(ctx); err != nil {
			r.desk.logger.Warn("chatrunner: desk recovery", "error", err.Error())
		}
	}
	r.holdInterrupted(ctx)
	return r, nil
}

// mode is a chat's deliberate mode, "" when it has none or no conversation
// yet: the desk's default Mode when steering is on.
func (r *Runner) mode(chatID string) string {
	ctx := context.Background()
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if err != nil {
		return ""
	}
	m, err := c.Mode(ctx)
	if err != nil {
		return ""
	}
	return m
}

// Agent returns the Nanite agent, for its slots, skills and notices. Use
// SetTools for the host's tools so their levels are kept.
func (r *Runner) Agent() *agent.Agent { return r.agent }

// Desk returns the front desk, or nil when Options.Desk is not set.
func (r *Runner) Desk() *Desk { return r.desk }

// Close stops running turns and releases the agent directory.
func (r *Runner) Close() error {
	if r.desk != nil {
		r.desk.stop()
	}
	r.mu.Lock()
	r.closed = true
	for _, h := range r.holds {
		if h.timer != nil {
			h.timer.Stop()
		}
	}
	r.mu.Unlock()
	r.stopNotices()
	<-r.noticesDone
	if r.desk != nil {
		r.desk.saveAll()
	}
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
// creating it on first use, and records which chat it serves (host state
// "chat") so a restarted runner can find its interrupted turns. A turn the
// previous process left unfinished is kept: see Interrupted, Resume and
// Discard.
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
	known := r.recorded[id]
	r.recorded[id] = true
	r.chats[id] = chatID
	r.mu.Unlock()
	if !known {
		// Only finding interrupted turns after a restart needs it.
		if err := c.SetHostState(ctx, chatHostState, chatID); err != nil {
			r.logger().Warn("chatrunner: record chat", "conversation", chatID, "error", err.Error())
			r.mu.Lock()
			delete(r.recorded, id)
			r.mu.Unlock()
		}
	}
	return c, nil
}

func (r *Runner) logger() *slog.Logger {
	if r.opts.Agent.Logger != nil {
		return r.opts.Agent.Logger
	}
	return slog.Default()
}

// discardInterrupted abandons a turn the previous process left unfinished,
// for the owner's new message: the owner has moved on. It returns the
// discarded turn's ID, "" when there was none.
func (r *Runner) discardInterrupted(ctx context.Context, chatID string, c *agent.Conversation) (string, error) {
	turn := c.Interrupted()
	if turn == nil {
		return "", nil
	}
	if err := c.Discard(ctx); err != nil {
		return "", fmt.Errorf("discard interrupted turn: %w", err)
	}
	r.endHold(chatID)
	return turn.ID, nil
}

type turnKey struct{}

// TurnFromContext returns the chat turn a host tool is running for.
func TurnFromContext(ctx context.Context) (chat.Turn, bool) {
	t, ok := ctx.Value(turnKey{}).(chat.Turn)
	return t, ok
}

// Start begins a turn. With a front desk, the message is planned first:
// a dispatched plan's background subtasks start before the turn, and the
// chat agent is told about the plan in the turn's context.
func (r *Runner) Start(ctx context.Context, turn chat.Turn) (chat.Run, error) {
	r.serve() // the Service calls Start, so it runs
	in := agent.Input{Text: turn.Text}
	if r.opts.Situation != nil {
		sit := r.opts.Situation(turn)
		in.Situation = &sit
	}
	text := contextText(turn.Context)
	var prelude []chatstream.Event
	x, err := r.beginAfterDesk(ctx, turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
		discarded, err := r.discardInterrupted(runCtx, turn.ConversationID, c)
		if err != nil {
			return nil, err
		}
		if discarded != "" {
			// Close the owner's offer: their new message replaced it.
			prelude = append(prelude, discardedActivity(discarded, "new message"))
		}
		halted := false
		if r.desk != nil && r.opts.Agent.Steering != nil {
			// Steer first: a reflex that halts answers the message, and the
			// desk must not plan it.
			prepared, err := c.Steer(runCtx, in)
			if err != nil {
				return nil, err
			}
			in.Steer, halted = prepared, prepared.Halted()
		}
		// The desk works only once the turn holds the conversation, so a
		// turn that could not start has not answered or planned anything.
		var plan *planRun
		if r.desk != nil && !halted {
			var note string
			note, prelude, plan = r.desk.beforeTurn(runCtx, turn.ConversationID, turn.Text)
			if note != "" {
				text = strings.TrimSpace(text + "\n\n" + note)
			}
		}
		if text != "" {
			in.Slots = []agent.SlotUpdate{{Slot: r.opts.ContextSlot, Entry: agent.SlotEntry{Owner: "chat", Key: "turn-context", Content: text}}}
		}
		if r.desk != nil {
			// Start what is ready (this message's plan, and later steps
			// that waited for the conversation) before the turn holds it.
			if err := r.desk.advanceWith(context.WithoutCancel(runCtx), turn.ConversationID, c); err != nil {
				r.desk.logger.Warn("chatrunner: desk dispatch before the turn", "conversation", turn.ConversationID, "error", err.Error())
				if plan != nil {
					prelude = append(prelude, activity(ActivityDeskPlan, map[string]any{"id": plan.id, "disposition": "failed", "error": err.Error()}))
				}
			}
		}
		return c.Send(runCtx, in)
	})
	if err != nil {
		return nil, err
	}
	x.prelude = prelude
	// A user turn lets notices start notice turns again.
	r.mu.Lock()
	delete(r.noticeTurns, x.nanite)
	r.mu.Unlock()
	return x, nil
}

// begin opens the chat conversation's Nanite conversation and starts a run
// on it with start, registered so its approvals are answered on the wire.
func (r *Runner) begin(ctx context.Context, turn chat.Turn, start func(*agent.Conversation, context.Context) (*agent.Run, error)) (*run, error) {
	return r.beginRun(ctx, turn, false, start)
}

// beginRun is begin; hostDispatch marks the front desk's own dispatch
// before the run is registered, so nothing sees it as the owner's turn.
func (r *Runner) beginRun(ctx context.Context, turn chat.Turn, hostDispatch bool, start func(*agent.Conversation, context.Context) (*agent.Run, error)) (*run, error) {
	c, err := r.conversation(ctx, turn.ConversationID)
	if err != nil {
		return nil, err
	}
	if hostDispatch {
		if held := c.Interrupted(); held != nil {
			// The owner decides first whether to resume or discard the
			// turn; the desk's work waits for that (the resumed run's end,
			// Discard, or the hold running out starts it again), as for any
			// held conversation. Nothing is registered, so no release wakes
			// the desk into retrying at once.
			r.deskWaits(turn.ConversationID, held.ID)
			return nil, fmt.Errorf("chatrunner: %w", agent.ErrBusy)
		}
	}
	out := &run{runner: r, turn: turn, nanite: c.ID(), pending: map[string]*pending{}, asked: map[string]bool{}, ended: make(chan struct{}), hostDispatch: hostDispatch}
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
	discarded, err := r.discardInterrupted(ctx, turn.ConversationID, c)
	if err != nil {
		return fmt.Errorf("chatrunner: %w", err)
	}
	if discarded != "" {
		r.announce(turn.ConversationID, discardedActivity(discarded, "new message"))
		if r.desk != nil {
			go r.desk.idle(turn.ConversationID) //nolint:gosec // G118: the desk's work outlives this call
		}
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
	released := r.active[x.nanite] == x
	if released {
		delete(r.active, x.nanite)
	}
	r.mu.Unlock()
	if released && r.desk != nil && x.turn.ConversationID != "" {
		// Start what waited for the conversation (an advance that found it
		// held) and publish what waited for the owner.
		go r.desk.idle(x.turn.ConversationID)
	}
}

// isHostDispatch reports whether a run is the front desk's own dispatch.
// The flag is set before the run is registered and never changes.
func (r *Runner) isHostDispatch(x *run) bool { return x.hostDispatch }

func (r *Runner) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// ownerTurn reports whether a turn the owner sees runs on a conversation:
// any run but the front desk's own dispatch.
func (r *Runner) ownerTurn(naniteID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	x := r.active[naniteID]
	return x != nil && !x.hostDispatch
}

// beginAfterDesk is begin, except that it waits (up to 10s) instead of
// failing the owner's turn when the conversation is held by a run the owner
// is not waiting on: the front desk's own short dispatch, or a run that has
// already sent its terminal event and is releasing.
// Do not call it from inside the Events loop of the run that holds the
// conversation: that run cannot release until the loop returns, so the call
// waits the full 10s and then fails busy.
func (r *Runner) beginAfterDesk(ctx context.Context, turn chat.Turn, start func(*agent.Conversation, context.Context) (*agent.Run, error)) (*run, error) {
	for range 2 {
		x, err := r.begin(ctx, turn, start)
		if err == nil || !errors.Is(err, agent.ErrBusy) {
			return x, err
		}
		held := r.activeRun(ConversationID(turn.ConversationID))
		if held == nil || (!r.isHostDispatch(held) && !held.finishing.Load()) {
			return nil, err
		}
		select {
		case <-held.ended:
		case <-time.After(10 * time.Second):
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.begin(ctx, turn, start)
}

// hostDispatch starts subtasks on a chat conversation for the front desk,
// holding the conversation like a turn but publishing nothing: background
// starts return their job handles at once, and their finishes arrive as
// notices.
func (r *Runner) hostDispatch(ctx context.Context, chatID string, starts []agent.DispatchStart) ([]agent.Dispatched, error) {
	x, err := r.beginRun(ctx, chat.Turn{ConversationID: chatID}, true, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
		return c.Dispatch(runCtx, starts)
	})
	if err != nil {
		return nil, err
	}
	defer close(x.ended)
	defer r.release(x)
	defer x.stop()
	for range x.inner.Events() {
	}
	result, err := x.inner.Wait()
	return result.Dispatched, err
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
