package engine

import (
	"context"
	"errors"
)

// ErrStopped means StopAll latched the agent off. Reopen it to accept work.
var ErrStopped = errors.New("nanite: agent stopped; close and reopen it to accept work")

// StopAll cancels every turn and job and prevents further starts. A cancelled
// caller only stops waiting; cancellation continues and the latch stays set.
func (e *Engine) StopAll(ctx context.Context) error {
	e.mu.Lock()
	if e.stopDone == nil {
		e.stopped = true
		e.stopCancel()
		e.stopDone = make(chan struct{})
		runs := make([]*Run, 0, len(e.active))
		for _, r := range e.active {
			runs = append(runs, r)
			r.Cancel()
		}
		e.panics.Go("stop all", func() {
			e.jobs.StopAll()
			for _, r := range runs {
				<-r.done
			}
			close(e.stopDone)
		}, nil)
	}
	done := e.stopDone
	e.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
