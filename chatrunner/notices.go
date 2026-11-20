package chatrunner

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"sync"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// noticePublishWait bounds how long one notice waits for the chat
// conversation's running turn to end before it is published.
const noticePublishWait = 2 * time.Minute

// maxNoticeTurns is how many notice turns may follow one another on a
// conversation before a user turn; after that, notices are only announced.
const maxNoticeTurns = 3

// listen hands each notice to its conversation's publisher, so one slow
// Observe never holds up another conversation's notices. With a front
// desk, notices go to it instead, and it publishes composed releases.
func (r *Runner) listen(ctx context.Context, notices <-chan agent.NoticePosted) {
	defer close(r.noticesDone)
	defer r.publishing.Wait()
	for n := range notices {
		if r.opts.Observe == nil {
			continue
		}
		if r.desk != nil {
			r.desk.notice(ctx, n)
			continue
		}
		r.enqueueNotice(n)
	}
}

// enqueueNotice queues a notice on its conversation's publisher, starting
// one when there is none.
func (r *Runner) enqueueNotice(n agent.NoticePosted) {
	ctx := r.noticeCtx
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx == nil || r.closed {
		return
	}
	p := r.publishers[n.ConversationID]
	if p == nil || !p.push(n) {
		// None yet, or the last one has just stopped: start another.
		p = &publisher{wake: make(chan struct{}, 1)}
		p.push(n)
		r.publishers[n.ConversationID] = p
		r.publishing.Add(1)
		go func(key string) {
			defer r.publishing.Done()
			p.run(ctx, r)
			r.mu.Lock()
			if r.publishers[key] == p {
				delete(r.publishers, key)
			}
			r.mu.Unlock()
		}(n.ConversationID)
	}
}

// publisher publishes one conversation's notices in order. It stops when it
// has been idle for publisherIdle; listen starts a new one when needed.
type publisher struct {
	mu    sync.Mutex
	queue []agent.NoticePosted
	done  bool
	wake  chan struct{}
}

// publisherIdle is how long a publisher waits for another notice before it
// stops.
var publisherIdle = time.Minute

// push queues a notice; false means the publisher has stopped and took
// nothing.
func (p *publisher) push(n agent.NoticePosted) bool {
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return false
	}
	p.queue = append(p.queue, n)
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return true
}

func (p *publisher) run(ctx context.Context, r *Runner) {
	idle := time.NewTimer(publisherIdle)
	defer idle.Stop()
	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.mu.Unlock()
			select {
			case <-p.wake:
				continue
			case <-ctx.Done():
				p.mu.Lock()
				p.done = true
				p.mu.Unlock()
				return
			case <-idle.C:
				p.mu.Lock()
				if len(p.queue) == 0 {
					p.done = true
					p.mu.Unlock()
					return
				}
				p.mu.Unlock()
				continue
			}
		}
		n := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()
		r.publish(ctx, n)
		idle.Reset(publisherIdle)
	}
}

// publish puts one notice on the wire, unless the runner's own running turn
// already carried it.
func (r *Runner) publish(ctx context.Context, n agent.NoticePosted) {
	if n.RunID != "" && r.activeRunID(n.ConversationID) == n.RunID {
		return // the runner's own running turn carried it and translates it
	}
	chatID := r.chatID(n.ConversationID)
	if chatID == "" {
		return // not a conversation this runner serves
	}
	publishCtx, cancel := context.WithTimeout(ctx, noticePublishWait)
	defer cancel()
	if err := r.publishNotice(publishCtx, chatID, n); err != nil && ctx.Err() == nil {
		// The notice still waits in the store for the next turn.
		logger := r.opts.Agent.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("chatrunner: publish notice", "conversation", chatID, "kind", n.Kind, "error", err.Error())
	}
}

// publishNotice publishes a notice as a notice turn (NoticeTurns, when the
// notice is not chained and the conversation has not had maxNoticeTurns in a
// row) or as a run that only announces it.
func (r *Runner) publishNotice(ctx context.Context, chatID string, n agent.NoticePosted) error {
	if r.opts.NoticeTurns && !n.Chained && r.takeNoticeTurn(n.ConversationID) {
		turn := chat.Turn{ConversationID: chatID, Text: agent.NoticeTurnText}
		return r.opts.Observe(ctx, chatID, turn, &noticeTurn{runner: r, turn: turn, notice: n})
	}
	return r.opts.Observe(ctx, chatID, chat.Turn{ConversationID: chatID}, &noticeRun{notice: n})
}

// takeNoticeTurn counts a notice turn for a conversation and reports whether
// it may start one: no more than maxNoticeTurns before a user turn resets
// the count.
func (r *Runner) takeNoticeTurn(naniteID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.noticeTurns[naniteID] >= maxNoticeTurns {
		return false
	}
	r.noticeTurns[naniteID]++
	return true
}

// noticeTurn is a notice turn that starts only once the chat Service has
// adopted it (on the first read of Events), so it never holds the
// conversation before it is published. If a turn started meanwhile, it only
// announces the notice.
type noticeTurn struct {
	runner *Runner
	turn   chat.Turn
	notice agent.NoticePosted

	mu       sync.Mutex
	started  *run
	canceled bool
}

func (n *noticeTurn) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		n.mu.Lock()
		if n.canceled {
			n.mu.Unlock()
			yield(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: chat.AbortCanceled}, nil)
			return
		}
		x, err := n.runner.begin(context.Background(), n.turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
			return c.Send(runCtx, agent.Input{Source: agent.SourceNotice})
		})
		if err == nil {
			x.notice = &n.notice
			n.started = x
		}
		n.mu.Unlock()
		if err != nil {
			// A turn began meanwhile: announce the notice only. The user
			// message the Service published says a notice arrived.
			for ev := range (&noticeRun{notice: n.notice}).Events() {
				if !yield(ev, nil) {
					return
				}
			}
			return
		}
		for ev, err := range x.Events() {
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (n *noticeTurn) Cancel() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.canceled = true
	if n.started != nil {
		n.started.Cancel()
	}
}

func (n *noticeTurn) Answer(ctx context.Context, approvalID string, d chat.Decision) error {
	n.mu.Lock()
	x := n.started
	n.mu.Unlock()
	if x == nil {
		return errors.New("chatrunner: the notice turn has not started")
	}
	return x.Answer(ctx, approvalID, d)
}

// noticeRun is a run that only announces a notice.
type noticeRun struct{ notice agent.NoticePosted }

func (n *noticeRun) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		if !yield(noticeActivity(n.notice), nil) {
			return
		}
		yield(chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: string(chatstream.FinishStop)}, nil)
	}
}

func (n *noticeRun) Cancel() {}

func (n *noticeRun) Answer(context.Context, string, chat.Decision) error {
	return errors.New("chatrunner: a notice asks for no approval")
}
