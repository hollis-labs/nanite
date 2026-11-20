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
// subscriber. Each subscriber has its own queue of up to NoticeQueueLimit
// notices; past that the oldest is dropped, and the next notice it receives
// says how many it lost (Dropped). The store still holds every notice.

// NoticeQueueLimit is how many unread notices one subscriber may hold.
const NoticeQueueLimit = 1024

// SubscribeNotices returns every notice posted from now on, in order, until
// ctx ends or the engine closes; then the channel is closed. A subscriber
// that reads slowly only delays itself, and loses the oldest of its queued
// notices past NoticeQueueLimit (reported in Dropped).
func (e *Engine) SubscribeNotices(ctx context.Context) <-chan events.NoticePosted {
	q := &noticeQueue{limit: e.noticeQueueLimit, wake: make(chan struct{}, 1), out: make(chan events.NoticePosted), done: make(chan struct{})}
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
	_, err := e.postNotice(ctx, conversation, kind, body, false)
	return err
}

// postNotice posts a notice and returns its ID; chained marks one caused by
// work a notice turn started.
func (e *Engine) postNotice(ctx context.Context, conversation, kind, body string, chained bool) (string, error) {
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
	e.announce(ctx, notice, chained)
	return notice.ID, nil
}

// publishNotice announces a notice the background job manager stored for a
// finished job.
func (e *Engine) publishNotice(job store.Job, notice store.Notice) {
	e.mu.Lock()
	chained := e.noticeJobs[job.ID]
	delete(e.noticeJobs, job.ID)
	e.mu.Unlock()
	e.announce(context.Background(), notice, chained)
}

// markNoticeWork records that a notice turn started a background job, so
// what that job causes is chained.
func (e *Engine) markNoticeWork(job string) {
	e.mu.Lock()
	e.noticeJobs[job] = true
	e.mu.Unlock()
}

// forgetNoticeWork drops a mark for a job that never started.
func (e *Engine) forgetNoticeWork(job string) {
	e.mu.Lock()
	delete(e.noticeJobs, job)
	e.mu.Unlock()
}

// noticeWork reports whether a notice turn started job.
func (e *Engine) noticeWork(job string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.noticeJobs[job]
}

// announce reports a stored notice on its conversation's running turn, if it
// has one, and to every subscriber, saying which turn (if any) carried it.
func (e *Engine) announce(ctx context.Context, notice store.Notice, chained bool) {
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
	ev := events.NoticePosted{ID: notice.ID, ConversationID: notice.ConversationID, Kind: notice.Kind, Body: notice.Body, Chained: chained}
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

// noticeQueue is one subscriber's queue, bounded by limit.
type noticeQueue struct {
	mu    sync.Mutex
	items []events.NoticePosted
	limit int
	// dropped counts notices lost since the last one delivered.
	dropped int
	wake    chan struct{}
	out     chan events.NoticePosted
	done    chan struct{}
	once    sync.Once
}

func (q *noticeQueue) push(ev events.NoticePosted) {
	q.mu.Lock()
	if len(q.items) >= q.limit {
		q.items = q.items[1:]
		q.dropped++
	}
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
		next.Dropped, q.dropped = q.dropped, 0
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
