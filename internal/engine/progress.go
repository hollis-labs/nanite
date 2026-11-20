package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// Least times between two progress events for one subtask. A change that
// arrives sooner is held and sent once the interval has passed, so the
// latest state always reaches the host. A background subtask reports less
// often: its progress lands in whatever turn its parent is running.
const (
	defaultProgressInterval           = 500 * time.Millisecond
	defaultBackgroundProgressInterval = 5 * time.Second
)

// progressEmitter delivers a subtask's events to whoever reports them: the
// parent's running turn for a foreground subtask, or the parent
// conversation's active run, if any, for a background one.
type progressEmitter func(ctx context.Context, event func(events.EventMeta) events.Event)

// followed names the subtask a follower reports on.
type followed struct {
	ID, ParentID, Variant string
	Depth                 int
	Background            bool
}

// follow drains a child run's events until it finishes, turning them into
// rate-limited SubtaskProgress events for the parent. A grandchild's
// lifecycle and progress, which reach this run from a lead, are passed up:
// its SubagentStarted and SubagentFinished unchanged, its progress under its
// own limiter. Held progress is sent before a SubagentFinished for the same
// subtask and before follow returns. It returns what run.Wait would.
// forward delivers the passed-up lifecycle events; it must not report them
// to the runtime sink again, since the lead's run already did.
func follow(ctx context.Context, run *Run, sub followed, interval time.Duration, emit, forward progressEmitter) (events.Result, error) {
	throttles := map[string]*throttle{}
	limiter := func(id string) *throttle {
		t := throttles[id]
		if t == nil {
			t = &throttle{interval: interval}
			throttles[id] = t
		}
		return t
	}
	send := func(p events.SubtaskProgress) {
		emit(ctx, func(meta events.EventMeta) events.Event { p.EventMeta = meta; return p })
	}
	pass := func(e events.Event) {
		forward(ctx, func(meta events.EventMeta) events.Event {
			switch e := e.(type) {
			case events.SubagentStarted:
				e.EventMeta = meta
				return e
			case events.SubagentFinished:
				e.EventMeta = meta
				return e
			}
			return e
		})
	}
	// flushAll sends every held state; with force it ignores the interval.
	flushAll := func(force bool) {
		now := time.Now()
		for _, t := range throttles {
			if p, ok := t.flush(now, force); ok {
				send(p)
			}
		}
	}
	var timer *time.Timer
	var wake <-chan time.Time
	arm := func() {
		due, ok := time.Time{}, false
		for _, t := range throttles {
			if d, held := t.due(); held && (!ok || d.Before(due)) {
				due, ok = d, true
			}
		}
		if !ok {
			wake = nil
			return
		}
		wait := max(time.Until(due), 0)
		if timer == nil {
			timer = time.NewTimer(wait)
		} else {
			timer.Reset(wait)
		}
		wake = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	offer := func(p events.SubtaskProgress) {
		if ready, ok := limiter(p.ID).offer(time.Now(), p); ok {
			send(ready)
		}
		arm()
	}
	own := events.SubtaskProgress{ID: sub.ID, ParentID: sub.ParentID, Variant: sub.Variant, Depth: sub.Depth, Background: sub.Background, StatusLine: "starting"}
	offer(own)
	handle := func(event events.Event) {
		switch e := event.(type) {
		case events.Question:
			// A caller following without a host has no way to answer.
			_ = run.Answer(e.ID, events.Answer{Value: []byte("null")})
		case events.ToolCallStarted:
			own.Step = max(int(run.steps.Load()), 1)
			own.Tool, own.StatusLine = e.Name, fmt.Sprintf("step %d: calling %s", own.Step, e.Name)
			offer(own)
		case events.SubtaskProgress:
			offer(e)
		case events.SubagentStarted:
			pass(e)
		case events.SubagentFinished:
			if t := throttles[e.ID]; t != nil {
				if p, ok := t.flush(time.Now(), true); ok {
					send(p)
				}
			}
			pass(e)
			arm()
		}
	}
	// finish handles what is still buffered once the run is done (its
	// events channel is closed by then), then sends every held state.
	finish := func() (events.Result, error) {
		for event := range run.events {
			handle(event)
		}
		<-run.done
		flushAll(true)
		return run.result, run.err
	}
	for {
		select {
		case <-run.done:
			return finish()
		case <-wake:
			if followWakeHook != nil {
				followWakeHook()
			}
			flushAll(false)
			arm()
		case event, open := <-run.events:
			if !open {
				return finish()
			}
			handle(event)
		}
	}
}

// followWakeHook, when set by a test, counts follow's timer wakes.
var followWakeHook func()

// throttle passes at most one progress state per interval, holding the
// latest one that arrives sooner. An interval of zero or less passes every
// state, so nothing is ever held.
type throttle struct {
	interval time.Duration
	last     time.Time
	pending  *events.SubtaskProgress
}

func (t *throttle) offer(now time.Time, p events.SubtaskProgress) (events.SubtaskProgress, bool) {
	if t.interval <= 0 || t.last.IsZero() || now.Sub(t.last) >= t.interval {
		t.last, t.pending = now, nil
		return p, true
	}
	t.pending = &p
	return events.SubtaskProgress{}, false
}

// due is when the held state may be sent; false when none is held.
func (t *throttle) due() (time.Time, bool) {
	if t.pending == nil {
		return time.Time{}, false
	}
	return t.last.Add(t.interval), true
}

// flush returns the held state once its interval has passed, or at once
// with force.
func (t *throttle) flush(now time.Time, force bool) (events.SubtaskProgress, bool) {
	if t.pending == nil || (!force && now.Sub(t.last) < t.interval) {
		return events.SubtaskProgress{}, false
	}
	p := *t.pending
	t.last, t.pending = now, nil
	return p, true
}

// subtaskLink is what a child's trace record names about the turn that
// started it.
type subtaskLink struct {
	ParentTurnID, ParentCallID string
	Depth                      int
}

// noteSubtask records a subtask this turn started for its trace record.
func (x *execution) noteSubtask(s trace.Subtask) {
	x.usageMu.Lock()
	defer x.usageMu.Unlock()
	x.subtasks = append(x.subtasks, s)
}

// traceSubtasks returns the subtasks this turn started.
func (x *execution) traceSubtasks() []trace.Subtask {
	x.usageMu.Lock()
	defer x.usageMu.Unlock()
	return append([]trace.Subtask(nil), x.subtasks...)
}

// traceUsage is a subtask's usage as its parent's record lists it; a lead's
// includes the subtasks it started.
func traceUsage(u events.Usage) trace.Usage {
	return trace.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens}
}

// traceStatus is a subtask's contract status as a trace status.
func traceStatus(s subagent.Status) trace.SubtaskStatus { return trace.SubtaskStatus(s) }

// subtaskLinkOf is the turn a running child was started from, for the
// child's trace record; nil for a root conversation.
func (e *Engine) subtaskLinkOf(child string) *subtaskLink {
	e.mu.Lock()
	defer e.mu.Unlock()
	link, ok := e.subtaskLinks[child]
	if !ok {
		return nil
	}
	return &link
}

// activeForwarder passes events another run already reported, such as a
// background lead's workers' lifecycle, to conversation's active run, if it
// has one, without reporting them to the runtime sink again.
func (e *Engine) activeForwarder(conversation string) progressEmitter {
	return func(ctx context.Context, event func(events.EventMeta) events.Event) {
		e.mu.Lock()
		r := e.active[conversation]
		e.mu.Unlock()
		if r == nil {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		r.forward(ctx, event)
	}
}

// activeEmitter reports progress on conversation's active run, when it has
// one: a background subtask's parent turn may long since have ended.
func (e *Engine) activeEmitter(conversation string) progressEmitter {
	return func(ctx context.Context, event func(events.EventMeta) events.Event) {
		e.mu.Lock()
		r := e.active[conversation]
		e.mu.Unlock()
		if r == nil {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		r.emit(ctx, event)
	}
}
