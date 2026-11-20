package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sort"
	"sync"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// ActivityInterrupted tells the owner about a turn the previous process left
// unfinished. Its value names the turn and either offers the actions
// (`actions`: resume, discard) or says what became of it (`disposition`:
// discarded, with `reason`: new message, timeout or requested).
const ActivityInterrupted = "nanite.interrupted"

// chatHostState is the host state on a Nanite conversation naming the chat
// conversation it serves; holdHostState is when its interrupted turn's hold
// began, keyed by turn ID, so a restart does not reset the clock or reuse
// a stale timestamp for a different interrupted turn.
const (
	chatHostState = "chat"
	holdHostState = "interrupted_since"
)

// defaultInterruptedHold is Options.InterruptedHold's default.
const defaultInterruptedHold = 15 * time.Minute

// hold is an interrupted turn waiting for the owner.
type hold struct {
	turn        string
	timer       *time.Timer // nil when the hold has no end
	deadline    time.Time
	retryDelay  time.Duration
	loggedRetry time.Duration
	noticeMu    sync.Mutex // orders waiting-item submission and withdrawal
	// waiting is set when the desk has work waiting for the turn; told,
	// once the owner has been told so.
	waiting, told bool
}

// discardedActivity says an interrupted turn was discarded, and why.
func discardedActivity(turn, reason string) chatstream.Event {
	return activity(ActivityInterrupted, map[string]any{"turn": turn, "disposition": "discarded", "reason": reason})
}

// announce publishes activity on a chat as a run of its own, in the
// background: the caller may be inside the chat Service.
func (r *Runner) announce(chatID string, events ...chatstream.Event) {
	if r.opts.Observe == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), noticePublishWait)
		defer cancel()
		if err := r.opts.Observe(ctx, chatID, chat.Turn{ConversationID: chatID}, &announceRun{events: events}); err != nil {
			r.logger().Warn("chatrunner: announce", "conversation", chatID, "error", err.Error())
		}
	}()
}

func (r *Runner) holdFor() time.Duration {
	switch {
	case r.opts.InterruptedHold == 0:
		return defaultInterruptedHold
	case r.opts.InterruptedHold < 0:
		return -1
	}
	return r.opts.InterruptedHold
}

// holdInterrupted starts a hold for every interrupted turn, from the time
// saved when it began (or now).
func (r *Runner) holdInterrupted(ctx context.Context) {
	chats, err := r.InterruptedChats(ctx)
	if err != nil {
		r.logger().Warn("chatrunner: find interrupted turns", "error", err.Error())
		return
	}
	for _, chatID := range chats {
		r.startHold(ctx, chatID)
	}
}

func (r *Runner) startHold(ctx context.Context, chatID string) {
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if err != nil {
		return
	}
	turn := c.Interrupted()
	if turn == nil {
		return
	}
	since := time.Now()
	var saved struct {
		Turn  string    `json:"turn"`
		Since time.Time `json:"since"`
	}
	raw, err := c.HostState(ctx, holdHostState)
	if err == nil && json.Unmarshal([]byte(raw), &saved) == nil && saved.Turn == turn.ID && !saved.Since.IsZero() {
		since = saved.Since
	} else {
		// Legacy timestamps without a turn ID cannot safely identify this hold;
		// give it a fresh deadline once, just as for a stale or malformed stamp.
		saved.Turn, saved.Since = turn.ID, since.UTC()
		encoded, _ := json.Marshal(saved)
		if err := c.SetHostState(ctx, holdHostState, string(encoded)); err != nil {
			r.logger().Warn("chatrunner: save interrupted hold", "conversation", chatID, "error", err.Error())
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.holds[chatID] != nil {
		return
	}
	h := &hold{turn: turn.ID}
	if d := r.holdFor(); d >= 0 {
		h.deadline = since.Add(d)
		h.timer = time.AfterFunc(max(0, time.Until(h.deadline)), func() { r.expireHold(chatID, turn.ID) })
	}
	r.holds[chatID] = h
	r.logger().Info("chatrunner: interrupted turn waits for the owner", "conversation", chatID, "turn", turn.ID)
}

// endHold ends a chat's hold: its turn was resumed or discarded.
func (r *Runner) endHold(chatID string) {
	r.mu.Lock()
	h := r.holds[chatID]
	delete(r.holds, chatID)
	r.mu.Unlock()
	if h == nil {
		return
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	if r.desk != nil {
		h.noticeMu.Lock()
		r.desk.withdraw(chatID, "interrupted-wait-"+h.turn)
		h.noticeMu.Unlock()
	}
	ctx := context.Background()
	if c, err := r.agent.Conversation(ctx, ConversationID(chatID)); err == nil {
		if err := c.SetHostState(ctx, holdHostState, ""); err != nil {
			r.logger().Warn("chatrunner: clear interrupted hold", "conversation", chatID, "error", err.Error())
		}
	}
}

// expireHold discards a turn the owner left interrupted for the whole hold,
// says so on the chat, and starts the desk's waiting work.
func (r *Runner) expireHold(chatID, turnID string) {
	if r.isClosed() {
		return
	}
	r.mu.Lock()
	resuming := r.resuming[chatID]
	h := r.holds[chatID]
	r.mu.Unlock()
	if h == nil || h.turn != turnID {
		return
	}
	if resuming {
		return // a successful resume ends the hold; unclaim re-arms failures
	}
	ctx := context.Background()
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if err != nil {
		r.rearmHold(chatID, err)
		return
	}
	if turn := c.Interrupted(); turn == nil || turn.ID != turnID {
		r.endHold(chatID)
		return
	}
	if err := c.Discard(ctx); err != nil {
		if errors.Is(err, agent.ErrNoInterrupted) {
			r.endHold(chatID)
		} else {
			r.rearmHold(chatID, err)
		}
		return
	}
	r.endHold(chatID)
	r.logger().Info("chatrunner: interrupted turn discarded after its hold", "conversation", chatID, "turn", turnID)
	r.announce(chatID, discardedActivity(turnID, "timeout"))
	if r.desk != nil {
		r.desk.idle(chatID)
	}
}

// deskWaits notes that the desk's work on a chat waits for its interrupted
// turn, and tells the owner once per hold when the chat can be published on.
func (r *Runner) deskWaits(chatID, turnID string) {
	r.mu.Lock()
	h := r.holds[chatID]
	r.mu.Unlock()
	if h == nil {
		// Not held yet (the turn was found after Open): hold it now.
		r.startHold(context.Background(), chatID)
	}
	r.mu.Lock()
	if h = r.holds[chatID]; h != nil && !h.waiting {
		h.waiting = true
		r.logger().Info("chatrunner: desk work waits for an interrupted turn", "conversation", chatID, "turn", turnID)
	}
	r.mu.Unlock()
	r.tellWaiting()
}

// tellWaiting tells the owner, once per hold, that desk work waits for an
// interrupted turn. Until the chat Service is known to run (RecoverTurns,
// or an owner's turn) it waits: a notice published before then is lost.
func (r *Runner) tellWaiting() {
	if r.desk == nil {
		return
	}
	type tell struct {
		chatID string
		h      *hold
	}
	var tells []tell
	r.mu.Lock()
	if r.serving {
		for chatID, h := range r.holds {
			if h.waiting && !h.told {
				h.told = true
				tells = append(tells, tell{chatID, h})
			}
		}
	}
	r.mu.Unlock()
	for _, t := range tells {
		go r.tellHeld(t.chatID, t.h)
	}
}

// tellHeld may run after endHold. Serialize with withdrawal and confirm
// that this exact hold still owns the chat before submitting its notice.
func (r *Runner) tellHeld(chatID string, h *hold) {
	h.noticeMu.Lock()
	defer h.noticeMu.Unlock()
	r.mu.Lock()
	current := r.holds[chatID] == h && !r.closed
	r.mu.Unlock()
	if current {
		r.desk.submit(chatID, desk.Item{
			ID: "interrupted-wait-" + h.turn, Kind: desk.ItemNotice, Source: "desk",
			Title: "Work is waiting",
			Text:  "Work the desk started waits for the interrupted turn: resume it or discard it to let the work continue.",
		})
	}
}

// serve notes that the chat Service runs, so the runner may publish.
func (r *Runner) serve() {
	r.mu.Lock()
	was := r.serving
	r.serving = true
	r.mu.Unlock()
	if !was {
		r.tellWaiting()
	}
}

// Interrupted reports the turn the previous process left unfinished on a
// chat conversation, or nil.
func (r *Runner) Interrupted(ctx context.Context, chatID string) (*agent.InterruptedTurn, error) {
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if errors.Is(err, agent.ErrNoConversation) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chatrunner: open conversation: %w", err)
	}
	return c.Interrupted(), nil
}

// InterruptedChats lists the chat conversations with a turn the previous
// process left unfinished. A conversation is found by the chat the runner
// recorded for it (host state "chat", saved on its first use since this
// version) or by front-desk state; one from an older version with neither
// is not listed.
func (r *Runner) InterruptedChats(ctx context.Context) ([]string, error) {
	chats, err := r.agent.ConversationHostStates(ctx, chatHostState)
	if err != nil {
		return nil, fmt.Errorf("chatrunner: list chats: %w", err)
	}
	r.mu.Lock()
	for naniteID, chatID := range r.chats {
		chats[naniteID] = chatID
	}
	r.mu.Unlock()
	var out []string
	for naniteID, chatID := range chats {
		c, err := r.agent.Conversation(ctx, naniteID)
		if err != nil {
			continue
		}
		if c.Interrupted() != nil {
			out = append(out, chatID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// RecoverTurns handles every turn the previous process left unfinished:
// with Options.ResumeInterrupted it resumes each; otherwise it offers each
// to the owner as a nanite.interrupted activity, to Resume or Discard. It
// needs Options.Observe, so call it once the chat Service the runner serves
// is running (Open cannot: the Service is built on the runner). A turn
// nobody resumes or discards is discarded when Options.InterruptedHold runs
// out. It returns the chat conversations it handled; a chat whose turn
// could not be resumed or offered is reported in the error.
func (r *Runner) RecoverTurns(ctx context.Context) ([]string, error) {
	if r.opts.Observe == nil {
		return nil, errors.New("chatrunner: RecoverTurns needs Options.Observe")
	}
	r.serve()
	chats, err := r.InterruptedChats(ctx)
	if err != nil {
		return nil, err
	}
	var handled []string
	var errs []error
	for _, chatID := range chats {
		r.mu.Lock()
		claimed := r.resuming[chatID]
		r.mu.Unlock()
		if claimed {
			continue // a Resume already has it
		}
		if r.opts.ResumeInterrupted {
			err = r.Resume(ctx, chatID)
		} else {
			err = r.offer(ctx, chatID)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", chatID, err))
			continue
		}
		handled = append(handled, chatID)
	}
	return handled, errors.Join(errs...)
}

// offer publishes a run that only tells the owner about the interrupted
// turn and what they can do with it.
func (r *Runner) offer(ctx context.Context, chatID string) error {
	turn, err := r.Interrupted(ctx, chatID)
	if err != nil {
		return err
	}
	if turn == nil {
		return nil
	}
	r.startHold(ctx, chatID)
	ev := activity(ActivityInterrupted, map[string]any{"turn": turn.ID, "actions": []string{"resume", "discard"}})
	return r.opts.Observe(ctx, chatID, chat.Turn{ConversationID: chatID}, &announceRun{events: []chatstream.Event{ev}})
}

// errNoInterrupted is Resume's error for a chat with no turn to resume
// (none, or one another Resume has claimed).
var errNoInterrupted = errors.New("no interrupted turn")

// Resume continues the turn the previous process left unfinished on a chat
// conversation, from its stored user message, as a new run published
// through Options.Observe. The front desk does not plan it again: it was
// planned (or not) when the owner sent it. The first Resume claims the
// turn; another, before the resumed run ends, fails with "no interrupted
// turn" without publishing anything. Resume returns once Observe has
// accepted the run, which waits while another run holds the chat; the
// resumed run's events follow on the wire.
func (r *Runner) Resume(ctx context.Context, chatID string) error {
	if r.opts.Observe == nil {
		return errors.New("chatrunner: Resume needs Options.Observe")
	}
	r.mu.Lock()
	if r.resuming[chatID] {
		r.mu.Unlock()
		return fmt.Errorf("chatrunner: %s: %w", chatID, errNoInterrupted)
	}
	r.resuming[chatID] = true
	r.mu.Unlock()
	turn, err := r.Interrupted(ctx, chatID)
	if err == nil && turn == nil {
		err = fmt.Errorf("chatrunner: %s: %w", chatID, errNoInterrupted)
	}
	if err != nil {
		r.unclaim(chatID)
		return err
	}
	// No text: the owner's message is already in the chat; the resumed run
	// answers it.
	t := chat.Turn{ConversationID: chatID}
	rt := &resumeTurn{runner: r, turn: t}
	rt.ctx, rt.stop = context.WithCancel(context.Background())
	if err := r.opts.Observe(ctx, chatID, t, rt); err != nil {
		rt.stop()
		r.unclaim(chatID)
		return err
	}
	return nil
}

func (r *Runner) unclaim(chatID string) {
	r.mu.Lock()
	delete(r.resuming, chatID)
	r.mu.Unlock()
	r.rearmHold(chatID, nil)
}

// rearmHold preserves the original deadline. Once expired, retries back off
// from 100ms to 30s; persistent failures log only once at each delay level.
func (r *Runner) rearmHold(chatID string, cause error) {
	r.mu.Lock()
	h := r.holds[chatID]
	if r.closed || h == nil || h.deadline.IsZero() {
		r.mu.Unlock()
		return
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	wait := time.Until(h.deadline)
	if wait <= 0 {
		h.retryDelay = min(max(100*time.Millisecond, h.retryDelay*2), 30*time.Second)
		wait = h.retryDelay
	}
	logFailure := cause != nil && h.loggedRetry != wait
	if logFailure {
		h.loggedRetry = wait
	}
	h.timer = time.AfterFunc(wait, func() { r.expireHold(chatID, h.turn) })
	r.mu.Unlock()
	if logFailure {
		r.logger().Warn("chatrunner: retry interrupted hold", "conversation", chatID, "retry_after", wait, "error", cause.Error())
	}
}

// Discard abandons the turn the previous process left unfinished on a chat
// conversation. The conversation then takes new turns, and the front desk's
// waiting work starts.
func (r *Runner) Discard(ctx context.Context, chatID string) error {
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if err != nil {
		return fmt.Errorf("chatrunner: open conversation: %w", err)
	}
	turn := c.Interrupted()
	if err := c.Discard(ctx); err != nil {
		if errors.Is(err, agent.ErrNoInterrupted) {
			r.endHold(chatID)
			return nil
		}
		return fmt.Errorf("chatrunner: discard interrupted turn: %w", err)
	}
	r.endHold(chatID)
	if turn != nil {
		r.announce(chatID, discardedActivity(turn.ID, "requested"))
	}
	if r.desk != nil {
		go r.desk.idle(chatID) //nolint:gosec // G118: the desk's work outlives this call
	}
	return nil
}

// resumeTurn resumes an interrupted turn once the chat Service has adopted
// it (on the first read of Events), so it never holds the conversation
// before it is published.
type resumeTurn struct {
	runner *Runner
	turn   chat.Turn
	// ctx is cancelled by Cancel, so a cancel reaches a resume still
	// waiting for the conversation.
	ctx  context.Context
	stop context.CancelFunc

	mu       sync.Mutex
	started  *run
	canceled bool
}

func (t *resumeTurn) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		chatID := t.turn.ConversationID
		defer t.runner.unclaim(chatID)
		defer t.stop()
		t.mu.Lock()
		canceled := t.canceled
		t.mu.Unlock()
		if canceled {
			yield(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: chat.AbortCanceled}, nil)
			return
		}
		x, err := t.runner.beginAfterDesk(t.ctx, t.turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
			return c.Resume(runCtx)
		})
		if err == nil {
			t.runner.endHold(chatID)
			t.mu.Lock()
			t.started = x
			canceled = t.canceled
			t.mu.Unlock()
			if canceled {
				x.Cancel()
			}
		}
		if err != nil {
			if t.ctx.Err() != nil {
				yield(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: chat.AbortCanceled}, nil)
				return
			}
			yield(chatstream.Event{Verb: chatstream.VerbRunError, Code: CodeTurnFailed, Message: "resume: " + err.Error()}, nil)
			return
		}
		for ev, err := range x.Events() {
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (t *resumeTurn) Cancel() {
	t.mu.Lock()
	t.canceled = true
	x := t.started
	t.mu.Unlock()
	t.stop()
	if x != nil {
		x.Cancel()
	}
}

func (t *resumeTurn) Answer(ctx context.Context, approvalID string, d chat.Decision) error {
	t.mu.Lock()
	x := t.started
	t.mu.Unlock()
	if x == nil {
		return errors.New("chatrunner: the resumed turn has not started")
	}
	return x.Answer(ctx, approvalID, d)
}

// announceRun is a run that only publishes some activity.
type announceRun struct{ events []chatstream.Event }

func (a *announceRun) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		for _, ev := range a.events {
			if !yield(ev, nil) {
				return
			}
		}
		yield(chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: string(chatstream.FinishStop)}, nil)
	}
}

func (a *announceRun) Cancel() {}

func (a *announceRun) Answer(context.Context, string, chat.Decision) error {
	return errors.New("chatrunner: an announcement asks for no approval")
}
