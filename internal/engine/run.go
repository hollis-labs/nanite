package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/events"
)

type Run struct {
	id     string
	events chan events.Event
	done   chan struct{}
	cancel context.CancelFunc
	mu     sync.Mutex
	seq    uint64
	result events.Result
	err    error
}

func (r *Run) ID() string                  { return r.id }
func (r *Run) Events() <-chan events.Event { return r.events }

func (r *Run) Wait() (events.Result, error) {
	for {
		select {
		case <-r.done:
			return r.result, r.err
		case _, open := <-r.events:
			if !open {
				<-r.done
				return r.result, r.err
			}
		}
	}
}

func (r *Run) Answer(string, events.Answer) error { return errors.New("nanite: no pending question") }
func (r *Run) Cancel()                            { r.cancel() }

func (r *Run) emit(ctx context.Context, event func(events.EventMeta) events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.events <- event(events.EventMeta{RunID: r.id, Seq: r.seq})
}

func (r *Run) finish(result events.Result, err error) {
	r.result, r.err = result, err
	r.seq++
	meta := events.EventMeta{RunID: r.id, Seq: r.seq}
	if err != nil {
		r.events <- events.RunFailed{EventMeta: meta, Err: err}
	} else {
		r.events <- events.RunFinished{EventMeta: meta, Result: result}
	}
	close(r.events)
	close(r.done)
}
