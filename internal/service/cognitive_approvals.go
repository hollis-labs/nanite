package service

import (
	"context"
	"errors"
	"sync"
	"time"

	permissionlib "github.com/hollis-labs/go-permission"
)

var (
	ErrCognitiveApprovalNotFound = errors.New("approval not found in this view")
	ErrCognitiveApprovalConflict = errors.New("approval is closed or has a different decision")
	ErrCognitiveApprovalScope    = errors.New("bound native approvals support only once scope")
)

// CognitiveApprovalDecision is the resource outcome, separate from a general
// request-key store. The host retains these alongside its volatile run state.
type CognitiveApprovalDecision struct {
	ApprovalID string                 `json:"approval_id"`
	ViewID     string                 `json:"session_view_id"`
	RunID      string                 `json:"run_id"`
	CallID     string                 `json:"call_id"`
	Decision   permissionlib.Decision `json:"decision"`
	Scope      permissionlib.Scope    `json:"scope"`
}

type cognitiveApproval struct {
	decision CognitiveApprovalDecision
	expires  time.Time
	closed   bool
	answered bool
}

// CognitiveApprovals binds native inband prompts to the exact run before
// publishing them. It accepts only once scope; it cannot broaden a tool grant.
type CognitiveApprovals struct {
	mu      sync.Mutex
	engine  *permissionlib.Engine
	records map[string]*cognitiveApproval
}

func NewCognitiveApprovals(engine *permissionlib.Engine) *CognitiveApprovals {
	return &CognitiveApprovals{engine: engine, records: make(map[string]*cognitiveApproval)}
}

// Request creates and binds a native prompt under the same lock used by both
// response facades. A retained response cannot reach the engine between
// request creation and publication of its run/call binding.
func (a *CognitiveApprovals) Request(viewID, runID, callID, tool string, input map[string]any, reason string) *permissionlib.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	req := a.engine.RequestApproval(viewID, tool, input, reason)
	a.bindLocked(req, runID, callID)
	return req
}

func (a *CognitiveApprovals) Bind(req *permissionlib.ApprovalRequest, runID, callID string) {
	if a == nil || req == nil || runID == "" || callID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bindLocked(req, runID, callID)
}

func (a *CognitiveApprovals) bindLocked(req *permissionlib.ApprovalRequest, runID, callID string) {
	if _, exists := a.records[req.ID]; exists {
		return
	}
	a.records[req.ID] = &cognitiveApproval{
		decision: CognitiveApprovalDecision{ApprovalID: req.ID, ViewID: req.SessionID, RunID: runID, CallID: callID},
		expires:  req.CreatedAt.Add(permissionlib.DefaultApprovalTimeout),
	}
}

// Finish closes unanswered prompts on timeout/cancellation. A successful
// response was already recorded under the same lock before the waiter resumed.
func (a *CognitiveApprovals) Finish(approvalID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if record := a.records[approvalID]; record != nil {
		record.closed = true
	}
}

func (a *CognitiveApprovals) Respond(ctx context.Context, viewID, approvalID string, decision permissionlib.Decision, scope permissionlib.Scope) (CognitiveApprovalDecision, error) {
	var zero CognitiveApprovalDecision
	if a == nil || a.engine == nil {
		return zero, ErrCognitiveApprovalNotFound
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.respondLocked(viewID, approvalID, decision, scope)
}

// RespondRetained preserves the retained facade for genuinely unbound
// requests. Bound requests always use the native resource outcome, including
// wrong-owner, closed and repeated requests; none falls back to the engine.
func (a *CognitiveApprovals) RespondRetained(ctx context.Context, viewID, approvalID string, decision permissionlib.Decision, scope permissionlib.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.records[approvalID] != nil {
		_, err := a.respondLocked(viewID, approvalID, decision, scope)
		return err
	}
	if a.engine == nil || !a.engine.Respond(approvalID, decision, scope, viewID) {
		return ErrCognitiveApprovalNotFound
	}
	return nil
}

func (a *CognitiveApprovals) respondLocked(viewID, approvalID string, decision permissionlib.Decision, scope permissionlib.Scope) (CognitiveApprovalDecision, error) {
	var zero CognitiveApprovalDecision
	record := a.records[approvalID]
	if record == nil || record.decision.ViewID != viewID {
		return zero, ErrCognitiveApprovalNotFound
	}
	if scope != permissionlib.ScopeOnce {
		return zero, ErrCognitiveApprovalScope
	}
	if record.answered {
		if record.decision.Decision == decision && record.decision.Scope == scope {
			return record.decision, nil
		}
		return zero, ErrCognitiveApprovalConflict
	}
	if record.closed || !time.Now().Before(record.expires) {
		return zero, ErrCognitiveApprovalConflict
	}
	if decision != permissionlib.DecisionAllow && decision != permissionlib.DecisionDeny {
		return zero, ErrCognitiveApprovalConflict
	}
	if !a.engine.Respond(approvalID, decision, scope, viewID) {
		record.closed = true
		return zero, ErrCognitiveApprovalConflict
	}
	record.decision.Decision, record.decision.Scope = decision, scope
	record.answered = true
	return record.decision, nil
}
