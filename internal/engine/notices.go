package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

// A notice is kept in the store until a turn of its conversation delivers it
// in the session slot. When it is posted it is also announced: as a
// NoticePosted event on the conversation's running turn, if any, and to every
// subscriber. Subscribers never lose one: each has its own unbounded queue.

// SubscribeNotices returns every notice posted from now on, in order, until
// ctx ends or the engine closes; then the channel is closed. A subscriber
// that reads slowly only delays itself.
func (e *Engine) SubscribeNotices(ctx context.Context) <-chan events.NoticePosted {
	q := &noticeQueue{wake: make(chan struct{}, 1), out: make(chan events.NoticePosted), done: make(chan struct{})}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		close(q.out)
		return q.out
	}
	e.noticeSeq++
	id := e.noticeSeq
	e.noticeSubs[id] = q
	e.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { e.unsubscribe(id) })
	e.panics.Go("notice subscription", func() {
		defer stop()
		q.pump()
	}, func(*panics.Error) { q.stop() })
	return q.out
}

func (e *Engine) unsubscribe(id uint64) {
	e.mu.Lock()
	q := e.noticeSubs[id]
	delete(e.noticeSubs, id)
	e.mu.Unlock()
	if q != nil {
		q.stop()
	}
}

// PostNotice posts a notice to a conversation: it waits in the store for the
// conversation's next turn and is announced now.
func (e *Engine) PostNotice(ctx context.Context, conversation, kind, body string) error {
	_, err := e.postNotice(ctx, conversation, kind, body)
	return err
}

// postNotice posts a notice and returns its ID.
func (e *Engine) postNotice(ctx context.Context, conversation, kind, body string) (string, error) {
	notice := store.Notice{ID: store.NewID(time.Now()), ConversationID: conversation, Kind: kind, Body: e.scrub.Text(body)}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return "", fmt.Errorf("nanite: agent is closed")
	}
	if err := e.cfg.Store.AddNotice(ctx, notice); err != nil {
		return "", err
	}
	e.announce(ctx, notice)
	return notice.ID, nil
}

// publishNotice announces a notice the background job manager stored.
func (e *Engine) publishNotice(notice store.Notice) {
	e.announce(context.Background(), notice)
}

// announce reports a stored notice on its conversation's running turn, if it
// has one, and to every subscriber, saying which turn (if any) carried it.
func (e *Engine) announce(ctx context.Context, notice store.Notice) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	r := e.active[notice.ConversationID]
	subs := make([]*noticeQueue, 0, len(e.noticeSubs))
	for _, q := range e.noticeSubs {
		subs = append(subs, q)
	}
	e.mu.Unlock()
	ev := events.NoticePosted{ID: notice.ID, ConversationID: notice.ConversationID, Kind: notice.Kind, Body: notice.Body}
	if r != nil {
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		if r.post(postCtx, func(meta events.EventMeta) events.Event { on := ev; on.EventMeta = meta; return on }) {
			ev.RunID = r.id
		}
		cancel()
	}
	for _, q := range subs {
		q.push(ev)
	}
}

// noticeQueue is one subscriber's unbounded queue.
type noticeQueue struct {
	mu    sync.Mutex
	items []events.NoticePosted
	wake  chan struct{}
	out   chan events.NoticePosted
	done  chan struct{}
	once  sync.Once
}

func (q *noticeQueue) push(ev events.NoticePosted) {
	q.mu.Lock()
	q.items = append(q.items, ev)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *noticeQueue) stop() { q.once.Do(func() { close(q.done) }) }

// pump delivers queued notices in order until the queue is stopped, then
// closes out.
func (q *noticeQueue) pump() {
	defer close(q.out)
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			q.mu.Unlock()
			select {
			case <-q.wake:
				continue
			case <-q.done:
				return
			}
		}
		next := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		select {
		case q.out <- next:
		case <-q.done:
			return
		}
	}
}

func deliveredNoticeIDs(report slots.Report, pending []store.Notice) []string {
	if len(pending) == 0 {
		return nil
	}
	shipped := make(map[string]bool)
	for _, d := range report[slots.Session].Decisions {
		if d.Owner == "engine" && d.Disposition == slots.Ship {
			shipped[d.Key] = true
		}
	}
	var delivered []string
	for _, n := range pending {
		if shipped["notice-"+n.ID] {
			delivered = append(delivered, n.ID)
		}
	}
	return delivered
}
