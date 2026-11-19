package engine

import (
	"context"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/events"
)

// Run streams the events and tracks the outcome of an active turn.
type Run struct {
	observe    func(context.Context, events.Event)
	id         string
	events     chan events.Event
	done       chan struct{}
	cancel     context.CancelFunc
	mu         sync.Mutex
	questionMu sync.Mutex
	questions  map[string]*pendingQuestion
	seq        uint64
	// closing is set once the terminal event is made; emit drops anything
	// after it, so the terminal event is always the last one reported.
	closing    bool
	result     events.Result
	err        error
	finishOnce sync.Once
}

// ID returns the turn identifier shared by its events.
func (r *Run) ID() string { return r.id }

// Events returns the ordered event stream, which closes when the run finishes.
func (r *Run) Events() <-chan events.Event { return r.events }

// Wait returns the final result and error. It drains unread events and
// submits a null answer to questions it encounters. Consume Events first
// when you need to observe events or answer questions.
func (r *Run) Wait() (events.Result, error) {
	for {
		select {
		case <-r.done:
			return r.result, r.err
		case event, open := <-r.events:
			if !open {
				<-r.done
				return r.result, r.err
			}
			if question, ok := event.(events.Question); ok {
				// A caller waiting without consuming events has no way to answer.
				_ = r.Answer(question.ID, events.Answer{Value: []byte("null")})
			}

		}
	}
}

// Cancel requests cancellation of the active turn.
func (r *Run) Cancel() { r.cancel() }

func (r *Run) emit(ctx context.Context, event func(events.EventMeta) events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return
	}
	// Reserve one buffer position for the terminal event even on cancellation.
	for len(r.events) >= cap(r.events)-1 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		return
	}
	r.seq++
	value := event(events.EventMeta{RunID: r.id, Seq: r.seq})
	r.report(ctx, value)
	r.events <- value
}

func (r *Run) report(ctx context.Context, event events.Event) {
	if r.observe != nil {
		r.observe(ctx, event)
	}
}

func (r *Run) terminal(result events.Result, err error) events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closing = true
	r.seq++
	meta := events.EventMeta{RunID: r.id, Seq: r.seq}
	if err != nil {
		return events.RunFailed{EventMeta: meta, Err: err}
	}
	return events.RunFinished{EventMeta: meta, Result: result}
}

// finish ends the run once; a later call, from the turn goroutine's panic
// handler, does nothing.
func (r *Run) finish(result events.Result, err error, event events.Event) {
	r.finishOnce.Do(func() {
		r.result, r.err = result, err
		r.events <- event
		close(r.events)
		close(r.done)
	})
}
