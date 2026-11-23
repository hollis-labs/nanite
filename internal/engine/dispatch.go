package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
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
//
// A host is not exempt from the agent's gates. Dispatch is refused when the
// agent does not offer the subtask tool, and a background start when it does
// not offer background_status. Each start is evaluated as a subtask call by
// the agent's rules and the host's PDP, as the root conversation: a deny
// refuses that start, and an ask goes to the gate on the dispatch run,
// attributed as dispatched. The host record lists each start as a tool call
// to subtask with its permission outcome.

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
	if !e.cfg.Builtins.Offers(subagent.ToolName) {
		return nil, fmt.Errorf("dispatch: this agent does not offer the subtask tool")
	}
	// Subtasks may copy the conversation's history: never from a history an
	// integrity check found unsafe.
	if err := e.runGate(ctx, id, false); err != nil {
		return nil, err
	}
	input := Input{}
	var err error
	ctx, err = e.prepareProfile(ctx, &input)
	if err != nil {
		return nil, err
	}
	variants, loadErr := subagent.Load(e.cfg.Dir)
	if p := profileFrom(ctx); p != nil {
		variants, loadErr = p.variants, nil
	}
	for i, start := range starts {
		if start.Background && !e.cfg.Builtins.Offers("background_status") {
			return nil, fmt.Errorf("dispatch start %d: background subtasks need the background tools, which this agent does not offer", i+1)
		}
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
	if e.stopped {
		return nil, ErrStopped
	}
	if err := e.checkDaily(); err != nil {
		return nil, err
	}
	if e.busyLocked(id) {
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
	var def definition.Definition
	var err error
	if p := profileFrom(ctx); p != nil {
		def = p.definition
	} else {
		def, err = definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	}
	if err == nil {
		err = e.cfg.Overrides.Apply(&def)
	}
	if err != nil {
		return events.Result{}, err
	}
	pdp, err := permission.Rules(def.Snapshot.Profile.Permissions.Mode, def.Rules)
	if err != nil {
		return events.Result{}, err
	}
	x := &execution{engine: e, run: r, info: info, definition: def, policy: pdp, variants: variants, root: info, lineage: []string{info.ID}}
	x.shared = sharedBudget(ctx, 0, e.cfg.TurnTokenBudget)
	tr.x = x
	out := make([]events.Dispatched, len(starts))
	calls := make([]trace.ToolCall, len(starts))
	var wg sync.WaitGroup
	for i, start := range starts {
		wg.Add(1)
		call := fmt.Sprintf("dispatch-%s-%d", r.id, i+1)
		e.panics.Go("dispatch start", func() {
			defer wg.Done()
			started := time.Now()
			calls[i] = trace.ToolCall{ToolCallID: call, Tool: subagent.ToolName, Permission: trace.PermissionNone, ErrorKind: trace.ErrorCancelled}
			defer func() { calls[i].DurationMS = time.Since(started).Milliseconds() }()
			decision, permit := x.authorizeStart(ctx, call, start)
			calls[i].Permission = permit
			if !decision.Allow {
				calls[i].ErrorKind = trace.ErrorDenied
				out[i] = events.Dispatched{Err: "denied: " + decision.Reason, IsError: true}
				return
			}
			callCtx := context.WithValue(context.WithValue(ctx, callIDKey{}, call), dispatchKey{}, true)
			var res tools.Result
			err := e.panics.Call("subtask", func() (err error) {
				res, err = x.subtask(callCtx, start)
				return err
			}, panics.With("conversation", info.ID), panics.With("call", call))
			calls[i].ErrorKind = ""
			switch {
			case err != nil:
				calls[i].ErrorKind = trace.ErrorTool
				if _, ok := panics.Ref(err); ok {
					calls[i].ErrorKind = trace.ErrorPanic
				} else if errors.Is(err, context.Canceled) {
					calls[i].ErrorKind = trace.ErrorCancelled
				}
				out[i] = events.Dispatched{Err: e.scrub.Text(err.Error()), IsError: true}
			case json.Valid([]byte(res.Text)):
				out[i] = events.Dispatched{Result: json.RawMessage(res.Text), IsError: res.IsError}
				if res.IsError {
					calls[i].ErrorKind = trace.ErrorTool
				}
			default:
				calls[i].ErrorKind = trace.ErrorTool
				out[i] = events.Dispatched{Err: "subtask returned no JSON result", IsError: true}
			}
		}, func(*panics.Error) {
			// The work is contained by the Call above; wg.Done has run.
		}, panics.With("conversation", info.ID), panics.With("call", call))
	}
	wg.Wait()
	x.toolCalls = append(x.toolCalls, calls...)
	x.usageMu.Lock()
	usage := x.result.Usage
	x.usageMu.Unlock()
	return events.Result{Usage: usage, Dispatched: out}, ctx.Err()
}

// authorizeStart evaluates one dispatched start as a subtask call by the root
// conversation: the agent's rules and the host's PDP decide, and an ask goes
// to the gate on the dispatch run, attributed as dispatched. An approval is
// always for that start only: a conversation or standing scope is narrowed
// to once, so it never grants the model's own subtask calls. permit is how
// the decision is recorded in the trace.
func (x *execution) authorizeStart(ctx context.Context, call string, start subagent.Request) (permission.PermissionDecision, trace.Permission) {
	e := x.engine
	raw, _ := json.Marshal(start)
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	origin := &permission.SubtaskOrigin{Variant: start.Variant, Goal: e.scrub.Text(start.Goal), Depth: 1, CallID: call, Dispatched: true}
	req := permission.PermissionRequest{ID: call, ConversationID: x.info.ID, TurnID: x.run.id, Tool: subagent.ToolName, Input: e.scrub.Bytes(raw), Subtask: origin}
	principal := "urn:nanite:agent:" + url.PathEscape(x.definition.Snapshot.Definition.DefinitionID)
	var effect policy.Decision
	err := e.panics.Call("permission policy", func() (err error) {
		effect, err = permission.Evaluate(ctx, e.cfg.Logger, e.cfg.PDP, x.policy, principal, x.info.Role, req, args, map[string]any{"dispatched": true})
		return err
	}, panics.With("conversation", x.info.ID), panics.With("call", call))
	decision := permission.PermissionDecision{Allow: effect.Effect == policy.Allow && err == nil, Reason: effect.Reason}
	if ref, ok := panics.Ref(err); ok {
		decision.Reason = "permission check failed (ref " + ref + ")"
	}
	permit := trace.PermissionAllow
	if effect.Effect == policy.Ask && err == nil {
		permit = trace.PermissionApproved
		if e.cfg.AutoApprove {
			decision = x.autoApprove(ctx, req)
		} else if x.granted(subagent.ToolName) {
			decision = permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation, Reason: "conversation approval"}
		} else {
			req.Reason = e.scrub.Text(effect.Reason)
			gateReq := req
			gateReq.Input = raw
			release, err := e.askTurn(ctx, x.root.ID)
			if err != nil {
				decision = permission.PermissionDecision{Reason: "approval not asked: " + err.Error()}
			} else {
				x.run.emit(ctx, func(meta events.EventMeta) events.Event { return events.PermissionAsked{EventMeta: meta, Request: req} })
				decision = x.decide(ctx, gateReq)
				release()
			}
			if decision.Allow && decision.Scope != permission.ScopeOnce {
				// An approval of a host's dispatch is for that start only:
				// a standing grant of subtask would also let the model's own
				// subtask calls run unasked.
				decision.Scope = permission.ScopeOnce
				decision.Reason = strings.TrimSpace(decision.Reason + " (a dispatch approval covers this start only)")
			}
		}
	}
	if !decision.Allow {
		permit = trace.PermissionDeny
	}
	decision.Reason = e.scrub.Text(decision.Reason)
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.PermissionResolved{EventMeta: meta, ID: call, Decision: decision}
	})
	return decision, permit
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
