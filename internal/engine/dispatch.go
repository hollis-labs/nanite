package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

// A host can start subtasks itself, without a model call: Dispatch. The
// subtasks go through exactly the path a model's subtask call takes (variant,
// budgets, slot limits, the turn's shared token budget, forked context, the
// result contract, relayed approvals, progress and trace links), from a
// dispatch run on the conversation. A dispatch run holds the conversation like
// a turn while it runs: it reports the subtasks' events, carries their relayed
// asks, and ends with every outcome in Result.Dispatched, in the order given.
//
// A foreground start runs to its end within the dispatch run; a background
// start returns its job handle at once and posts a notice when it finishes.
// The dispatch run itself makes no model request and adds nothing to the
// conversation's history; its trace record has source "host".
//
// A dispatched subtask's forked context is the conversation's latest settled
// messages: there is no in-flight model call to leave out.

// dispatchKey marks the context of a subtask a host started with Dispatch.
type dispatchKey struct{}

// ErrNoStarts is returned by Dispatch when it is given nothing to start.
var ErrNoStarts = errors.New("nanite: dispatch needs at least one start")

// Dispatch starts subtasks on a root conversation for the host. Every start
// is checked before anything runs; a bad one fails the whole call.
func (e *Engine) Dispatch(ctx context.Context, id string, starts []subagent.Request) (*Run, error) {
	if len(starts) == 0 {
		return nil, ErrNoStarts
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	variants, loadErr := subagent.Load(e.cfg.Dir)
	for i, start := range starts {
		if err := checkStart(start, variants); err != nil {
			if loadErr != nil {
				err = fmt.Errorf("%w (some variants did not load: %w)", err, loadErr)
			}
			return nil, fmt.Errorf("dispatch start %d: %w", i+1, err)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("nanite: agent is closed")
	}
	if e.active[id] != nil {
		return nil, ErrBusy
	}
	info, err := e.cfg.Store.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	if info.ParentID != "" {
		return nil, fmt.Errorf("dispatch starts subtasks on a root conversation; %s is a subtask", id)
	}
	interrupted, err := e.cfg.Store.Interrupted(ctx, id)
	if err != nil {
		return nil, err
	}
	if interrupted != nil {
		return nil, fmt.Errorf("conversation has an interrupted turn; resume or discard it before dispatching")
	}
	r, runCtx := e.newRunLocked(ctx, id, store.NewID(time.Now()))
	starts = append([]subagent.Request(nil), starts...)
	e.panics.Go("dispatch", func() {
		e.executeDispatch(runCtx, r, info, maps.Clone(variants), starts)
	}, func(pe *panics.Error) {
		r.cancel()
		e.release(id, r, func() {
			r.finish(events.Result{}, pe, events.RunFailed{EventMeta: events.EventMeta{RunID: r.id}, Err: pe})
		})
	}, panics.With("conversation", id), panics.With("run", r.id))
	return r, nil
}

// checkStart checks what a subtask call would check before it starts.
func checkStart(start subagent.Request, variants map[string]subagent.Variant) error {
	if err := start.Validate(variants); err != nil {
		return err
	}
	if _, err := start.Schema(); err != nil {
		return err
	}
	_, err := start.ContextSpec()
	return err
}

func (e *Engine) executeDispatch(ctx context.Context, r *Run, info store.Conversation, variants map[string]subagent.Variant, starts []subagent.Request) {
	ctx, endSpan := events.TurnSpan(ctx)
	tr := &turnTrace{rec: newTraceRecord(time.Now().UTC(), r.id, info, false, nil)}
	tr.rec.Source = trace.SourceHost
	var result events.Result
	err := e.panics.Call("dispatch", func() (err error) {
		result, err = e.dispatch(ctx, r, info, variants, starts, tr)
		return err
	}, panics.With("conversation", info.ID), panics.With("run", r.id))
	if err != nil {
		if clean := e.scrub.Text(err.Error()); clean != err.Error() {
			err = errors.New(clean)
		}
	}
	_ = e.panics.Call("trace", func() error { e.writeTrace(ctx, tr.rec, tr.x, result, err); return nil }, panics.With("conversation", info.ID), panics.With("run", r.id))
	endSpan()
	terminal := r.terminal(result, err)
	r.report(context.WithoutCancel(ctx), terminal)
	r.cancel()
	e.release(info.ID, r, func() { r.finish(result, err, terminal) })
}

func (e *Engine) dispatch(ctx context.Context, r *Run, info store.Conversation, variants map[string]subagent.Variant, starts []subagent.Request, tr *turnTrace) (events.Result, error) {
	r.emit(ctx, func(meta events.EventMeta) events.Event { return events.RunStarted{EventMeta: meta} })
	x := &execution{engine: e, run: r, info: info, variants: variants, root: info, lineage: []string{info.ID}}
	x.shared = sharedBudget(ctx, 0, e.cfg.TurnTokenBudget)
	tr.x = x
	out := make([]events.Dispatched, len(starts))
	var wg sync.WaitGroup
	for i, start := range starts {
		wg.Add(1)
		call := fmt.Sprintf("dispatch-%s-%d", r.id, i+1)
		e.panics.Go("dispatch start", func() {
			defer wg.Done()
			callCtx := context.WithValue(context.WithValue(ctx, callIDKey{}, call), dispatchKey{}, true)
			var res tools.Result
			err := e.panics.Call("subtask", func() (err error) {
				res, err = x.subtask(callCtx, start)
				return err
			}, panics.With("conversation", info.ID), panics.With("call", call))
			switch {
			case err != nil:
				out[i] = events.Dispatched{Err: e.scrub.Text(err.Error()), IsError: true}
			case json.Valid([]byte(res.Text)):
				out[i] = events.Dispatched{Result: json.RawMessage(res.Text), IsError: res.IsError}
			default:
				out[i] = events.Dispatched{Err: "subtask returned no JSON result", IsError: true}
			}
		}, func(*panics.Error) {
			// The work is contained by the Call above; wg.Done has run.
		}, panics.With("conversation", info.ID), panics.With("call", call))
	}
	wg.Wait()
	x.usageMu.Lock()
	usage := x.result.Usage
	x.usageMu.Unlock()
	return events.Result{Usage: usage, Dispatched: out}, ctx.Err()
}

// dispatchedChain reports whether a host dispatch started the subtask chain
// above a child: any of its non-root ancestors, or itself, was dispatched.
func (e *Engine) dispatchedChain(lineage []string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range lineage {
		if e.dispatched[id] {
			return true
		}
	}
	return false
}

// Job is a background job of a conversation as background_status reports it.
func (e *Engine) Job(ctx context.Context, conversation, id string) (string, error) {
	x := &execution{engine: e, info: store.Conversation{ID: conversation}}
	job, err := x.job(ctx, id)
	if err != nil {
		return "", err
	}
	return x.jobOutput(job)
}
