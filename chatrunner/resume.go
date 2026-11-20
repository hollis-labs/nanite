package chatrunner

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"sync"

	chat "github.com/hollis-labs/libs/agent/chat"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// ActivityInterrupted offers the owner a turn the previous process left
// unfinished: its value names the turn and the actions, resume and discard.
const ActivityInterrupted = "nanite.interrupted"

// chatHostState is the host state on a Nanite conversation naming the chat
// conversation it serves.
const chatHostState = "chat"

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
// process left unfinished.
func (r *Runner) InterruptedChats(ctx context.Context) ([]string, error) {
	chats, err := r.agent.ConversationHostStates(ctx, chatHostState)
	if err != nil {
		return nil, fmt.Errorf("chatrunner: list chats: %w", err)
	}
	// A conversation from before the runner recorded its chat: a safe chat
	// ID is its own Nanite ID less the prefix.
	infos, err := r.agent.Conversations(ctx)
	if err != nil {
		return nil, fmt.Errorf("chatrunner: list conversations: %w", err)
	}
	for _, info := range infos {
		if _, ok := chats[info.ID]; ok {
			continue
		}
		if id, ok := strings.CutPrefix(info.ID, "chat-"); ok && ConversationID(id) == info.ID {
			chats[info.ID] = id
		}
	}
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
// is running (Open cannot: the Service is built on the runner). It returns
// the chat conversations it handled; a chat whose turn could not be resumed
// or offered is left for a later call and reported in the error.
func (r *Runner) RecoverTurns(ctx context.Context) ([]string, error) {
	if r.opts.Observe == nil {
		return nil, errors.New("chatrunner: RecoverTurns needs Options.Observe")
	}
	chats, err := r.InterruptedChats(ctx)
	if err != nil {
		return nil, err
	}
	var handled []string
	var errs []error
	for _, chatID := range chats {
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
	ev := activity(ActivityInterrupted, map[string]any{"turn": turn.ID, "actions": []string{"resume", "discard"}})
	return r.opts.Observe(ctx, chatID, chat.Turn{ConversationID: chatID}, &announceRun{events: []chatstream.Event{ev}})
}

// Resume continues the turn the previous process left unfinished on a chat
// conversation, from its stored user message, as a new run published
// through Options.Observe. The front desk does not plan it again: it was
// planned (or not) when the owner sent it. It returns once the run is
// published; its events follow on the wire.
func (r *Runner) Resume(ctx context.Context, chatID string) error {
	if r.opts.Observe == nil {
		return errors.New("chatrunner: Resume needs Options.Observe")
	}
	turn, err := r.Interrupted(ctx, chatID)
	if err != nil {
		return err
	}
	if turn == nil {
		return fmt.Errorf("chatrunner: %s has no interrupted turn", chatID)
	}
	// No text: the owner's message is already in the chat; the resumed run
	// answers it.
	t := chat.Turn{ConversationID: chatID}
	return r.opts.Observe(ctx, chatID, t, &resumeTurn{runner: r, turn: t})
}

// Discard abandons the turn the previous process left unfinished on a chat
// conversation. The conversation then takes new turns, and the front desk's
// waiting work starts.
func (r *Runner) Discard(ctx context.Context, chatID string) error {
	c, err := r.agent.Conversation(ctx, ConversationID(chatID))
	if err != nil {
		return fmt.Errorf("chatrunner: open conversation: %w", err)
	}
	if err := c.Discard(ctx); err != nil {
		return fmt.Errorf("chatrunner: discard interrupted turn: %w", err)
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

	mu       sync.Mutex
	started  *run
	canceled bool
}

func (t *resumeTurn) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		t.mu.Lock()
		if t.canceled {
			t.mu.Unlock()
			yield(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: chat.AbortCanceled}, nil)
			return
		}
		x, err := t.runner.beginAfterDesk(context.Background(), t.turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
			return c.Resume(runCtx)
		})
		if err == nil {
			t.started = x
		}
		t.mu.Unlock()
		if err != nil {
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
	defer t.mu.Unlock()
	t.canceled = true
	if t.started != nil {
		t.started.Cancel()
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
