package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
)

// A subtask has no person of its own to ask. An approval its grants do not
// cover, and an MCP elicitation, is relayed to the root conversation's ask
// path: it is reported on the root's active run, attributed to the subtask,
// and only the asking child waits for the answer.
//
// One ask is open per root conversation at a time (root asks, relayed asks
// and relayed questions alike), so prompts never overlap and an answer cannot
// land on the wrong card.
//
// The root may have no run when a background subtask asks (its turn ended).
// The ask then waits for the root's next run, and a notice tells the root
// conversation that it is waiting. If the root's run ends while the ask is
// open and nobody answered, it is asked again on the next run. The wait ends
// when the subtask is stopped, or with a denial "unanswered" after
// Config.AskWait without a root run.

// DefaultAskWait bounds how long a relayed ask waits, in total, for the root
// conversation to have a running turn.
const DefaultAskWait = 30 * time.Minute

// errRootEnded is the cause given to an ask's context when the root run it
// was relayed to ends before the ask is answered.
var errRootEnded = errors.New("the root conversation's turn ended before the ask was answered")

// errUnanswered ends a relayed ask that waited AskWait for a root run.
var errUnanswered = errors.New("unanswered")

// askGap is a pause on the root's own approval path between reporting the ask
// and putting it to the gate; a test sets it to widen the window an ordering
// bug (reporting an ask before holding the ask slot) would need.
var askGap time.Duration

// decide asks the host's gate. The caller holds the root's ask slot. A gate
// failure denies.
func (x *execution) decide(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
	var decision permission.PermissionDecision
	err := x.engine.panics.Call("approval gate", func() (err error) {
		decision, err = x.engine.cfg.Gate.Decide(ctx, req)
		return err
	}, panics.With("conversation", req.ConversationID), panics.With("call", req.ID))
	if err != nil {
		return permission.PermissionDecision{Reason: "approval gate failed"}
	}
	return decision
}

// relayApproval asks the root conversation's gate for a subtask's call. req
// is the scrubbed request for events; gateReq carries the raw input.
func (x *execution) relayApproval(ctx context.Context, req, gateReq permission.PermissionRequest) permission.PermissionDecision {
	// authorize handles auto mode before entering the relay, so this path
	// always needs a real answer from the root.
	req.ConversationID, gateReq.ConversationID = x.root.ID, x.root.ID
	req.ID = x.relayID(req.ID)
	gateReq.ID = req.ID
	origin := *x.origin
	req.Subtask, gateReq.Subtask = &origin, &origin
	wait := x.waiter()
	defer wait.withdraw()
	for {
		root, err := wait.root(ctx)
		if err != nil {
			reason := "the subtask stopped before its approval was answered"
			if errors.Is(err, errUnanswered) {
				reason = "unanswered"
			}
			return permission.PermissionDecision{Reason: reason}
		}
		askCtx, stop := whileRunning(ctx, root)
		decision, asked := x.askRoot(askCtx, root, req, gateReq)
		rootEnded := errors.Is(context.Cause(askCtx), errRootEnded)
		stop()
		if !asked || (!decision.Allow && !decision.Human && rootEnded && ctx.Err() == nil) {
			if ctx.Err() != nil {
				return permission.PermissionDecision{Reason: "the subtask stopped before its approval was answered"}
			}
			// The root's turn ended before the ask was put, or before anyone
			// answered it: ask again on the root's next turn.
			continue
		}
		if !decision.Allow && decision.Human && ctx.Err() == nil {
			x.engine.noteDenial(x.info.ID, req.Tool, x.engine.scrub.Text(decision.Reason))
		}
		return decision
	}
}

// askRoot puts a relayed ask on the root's run while holding the root's ask
// slot, from PermissionAsked through PermissionResolved, so the root's
// stream never shows two open asks. asked is false when the slot could not be
// taken (the root's turn ended, or the subtask stopped).
func (x *execution) askRoot(ctx context.Context, root *Run, req, gateReq permission.PermissionRequest) (permission.PermissionDecision, bool) {
	release, err := x.engine.askTurn(ctx, x.root.ID)
	if err != nil {
		return permission.PermissionDecision{}, false
	}
	defer release()
	root.emit(ctx, func(meta events.EventMeta) events.Event { return events.PermissionAsked{EventMeta: meta, Request: req} })
	decision := x.decide(ctx, gateReq)
	resolved := decision
	resolved.Reason = x.engine.scrub.Text(resolved.Reason)
	// Reported even when the root's turn has ended meanwhile (emit drops it).
	root.emit(context.WithoutCancel(ctx), func(meta events.EventMeta) events.Event {
		return events.PermissionResolved{EventMeta: meta, ID: req.ID, Decision: resolved}
	})
	return decision, true
}

// relayQuestion asks a subtask's MCP elicitation on the root conversation's
// run, attributed to the subtask.
func (x *execution) relayQuestion(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	origin := *x.origin
	prompt, display := x.engine.scrub.Text(prompt), x.engine.scrub.Bytes(schema)
	wait := x.waiter()
	defer wait.withdraw()
	for {
		root, err := wait.root(ctx)
		if err != nil {
			return nil, err
		}
		askCtx, stop := whileRunning(ctx, root)
		answer, err := func() (json.RawMessage, error) {
			release, err := x.engine.askTurn(askCtx, x.root.ID)
			if err != nil {
				return nil, err
			}
			defer release()
			return root.ask(askCtx, prompt, slices.Clone(schema), display, &origin)
		}()
		rootEnded := errors.Is(context.Cause(askCtx), errRootEnded)
		stop()
		if err != nil && rootEnded && ctx.Err() == nil {
			continue
		}
		return answer, err
	}
}

// relayID is a relayed ask's ID: the child's conversation ID and its call ID,
// so it never collides with a call in the root or another subtask.
func (x *execution) relayID(call string) string { return x.info.ID + ":" + call }

// rootWaiter finds the root's running turn for one relayed ask, keeping the
// ask's total wait for a turn under the engine's AskWait.
type rootWaiter struct {
	x        *execution
	left     time.Duration
	notified bool
	notice   string // the "is waiting" notice's ID, once posted
}

// withdraw takes back the "is waiting" notice when the ask ended before a
// root turn showed it. While a root turn runs it may already have read the
// notice, so it stays; a finished turn has marked it delivered, which
// WithdrawNotice leaves alone. The engine lock keeps a turn from starting
// between the check and the withdrawal.
func (w *rootWaiter) withdraw() {
	if w.notice == "" {
		return
	}
	e := w.x.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[w.x.root.ID] != nil {
		return
	}
	_ = e.cfg.Store.WithdrawNotice(context.Background(), w.x.root.ID, w.notice)
}

func (x *execution) waiter() *rootWaiter {
	wait := x.engine.cfg.AskWait
	if wait <= 0 {
		wait = DefaultAskWait
	}
	return &rootWaiter{x: x, left: wait}
}

// root returns the root conversation's active run, waiting for one when there
// is none. The first wait posts a notice to the root conversation.
func (w *rootWaiter) root(ctx context.Context) (*Run, error) {
	e := w.x.engine
	for {
		e.mu.Lock()
		r, changed, closed := e.active[w.x.root.ID], e.changed, e.closed
		e.mu.Unlock()
		if closed {
			return nil, errors.New("nanite: agent is closed")
		}
		if r != nil && !finished(r) {
			return r, nil
		}
		if !w.notified {
			w.notified = true
			body := fmt.Sprintf("Subtask %s (%s, %q) is waiting for an answer; it will be asked on the next turn.",
				w.x.info.ID, w.x.origin.Variant, w.x.origin.Goal)
			w.notice, _ = e.postNotice(context.WithoutCancel(ctx), w.x.root.ID, "subtask_ask", body, e.noticeWorkIn(w.x.lineage))
		}
		if w.left <= 0 {
			return nil, errUnanswered
		}
		start := time.Now()
		timer := time.NewTimer(w.left)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-changed:
		case <-timer.C:
		}
		timer.Stop()
		w.left -= time.Since(start)
	}
}

// askTurn takes the root conversation's single ask slot, waiting for it.
func (e *Engine) askTurn(ctx context.Context, root string) (func(), error) {
	e.mu.Lock()
	slot := e.askSlots[root]
	if slot == nil {
		slot = make(chan struct{}, 1)
		e.askSlots[root] = slot
	}
	e.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// signalLocked wakes everything waiting for a run to start or end. e.mu must
// be held.
func (e *Engine) signalLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

// whileRunning is ctx, also cancelled with cause errRootEnded once run
// finishes.
func whileRunning(ctx context.Context, run *Run) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(ctxDone(run.done), func() { cancel(errRootEnded) })
	return ctx, func() { stop(); cancel(nil) }
}

func finished(r *Run) bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// granted reports whether a conversation-scope allow covers tool for this
// conversation: one given on it or any conversation above it. Grants flow
// down a subtask chain, never up or across.
func (x *execution) granted(tool string) bool {
	e := x.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range x.lineage {
		if e.grants[id][tool] {
			return true
		}
	}
	return false
}

// grant records an allow with conversation scope on one conversation.
func (e *Engine) grant(conversation, tool string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.grants[conversation] == nil {
		e.grants[conversation] = make(map[string]bool)
	}
	e.grants[conversation][tool] = true
}

// denial is an approval a person refused a subtask.
type denial struct{ tool, reason string }

// noteDenial records that a person refused a subtask's relayed ask.
func (e *Engine) noteDenial(child, tool, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.denials[child] = append(e.denials[child], denial{tool, reason})
}

// takeDenials returns and forgets the approvals a person refused a subtask.
func (e *Engine) takeDenials(child string) []denial {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.denials[child]
	delete(e.denials, child)
	return d
}

// denialReason is "approval denied for <tool>: <reason>", one per refusal.
func denialReason(denied []denial) string {
	parts := make([]string, 0, len(denied))
	for _, d := range denied {
		part := "approval denied for " + d.tool
		if d.reason != "" {
			part += ": " + d.reason
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// ctxDone adapts a done channel to a context, for context.AfterFunc.
type ctxDone <-chan struct{}

func (c ctxDone) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c ctxDone) Done() <-chan struct{}       { return c }
func (c ctxDone) Err() error {
	select {
	case <-c:
		return context.Canceled
	default:
		return nil
	}
}
func (c ctxDone) Value(any) any { return nil }
