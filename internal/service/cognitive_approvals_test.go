package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	permissionlib "github.com/hollis-labs/go-permission"
)

func TestCognitiveApprovalsOwnershipAndRepetition(t *testing.T) {
	engine := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	approvals := NewCognitiveApprovals(engine)
	req := engine.RequestApproval("view", "dev_write", nil, "permission required")
	approvals.Bind(req, "run", "call")
	if _, err := approvals.Respond(t.Context(), "other", req.ID, permissionlib.DecisionAllow, permissionlib.ScopeOnce); !errors.Is(err, ErrCognitiveApprovalNotFound) {
		t.Fatalf("wrong view: %v", err)
	}
	first, err := approvals.Respond(t.Context(), "view", req.ID, permissionlib.DecisionAllow, permissionlib.ScopeOnce)
	if err != nil || first.RunID != "run" || first.CallID != "call" {
		t.Fatalf("response=%+v err=%v", first, err)
	}
	response := engine.WaitForApproval(t.Context(), req)
	approvals.Finish(req.ID)
	if response.Decision != permissionlib.DecisionAllow || response.Scope != permissionlib.ScopeOnce {
		t.Fatalf("waiter received %+v", response)
	}
	repeat, err := approvals.Respond(t.Context(), "view", req.ID, permissionlib.DecisionAllow, permissionlib.ScopeOnce)
	if err != nil || repeat != first {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}
	if _, err := approvals.Respond(t.Context(), "view", req.ID, permissionlib.DecisionDeny, permissionlib.ScopeOnce); !errors.Is(err, ErrCognitiveApprovalConflict) {
		t.Fatalf("conflicting decision: %v", err)
	}
}

func TestCognitiveApprovalsConcurrentConflictAndExpiry(t *testing.T) {
	engine := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	approvals := NewCognitiveApprovals(engine)
	req := engine.RequestApproval("view", "dev_write", nil, "permission required")
	approvals.Bind(req, "run", "call")
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, decision := range []permissionlib.Decision{permissionlib.DecisionAllow, permissionlib.DecisionDeny} {
		group.Go(func() {
			<-start
			_, err := approvals.Respond(t.Context(), "view", req.ID, decision, permissionlib.ScopeOnce)
			results <- err
		})
	}
	close(start)
	group.Wait()
	first, second := <-results, <-results
	if (first != nil || !errors.Is(second, ErrCognitiveApprovalConflict)) && (second != nil || !errors.Is(first, ErrCognitiveApprovalConflict)) {
		t.Fatalf("conflicting responders: %v, %v", first, second)
	}
	_ = engine.WaitForApproval(t.Context(), req)
	approvals.Finish(req.ID)

	expired := engine.RequestApproval("view", "dev_write", nil, "expired")
	expired.CreatedAt = time.Now().Add(-permissionlib.DefaultApprovalTimeout - time.Second)
	approvals.Bind(expired, "expired-run", "expired-call")
	if _, err := approvals.Respond(t.Context(), "view", expired.ID, permissionlib.DecisionAllow, permissionlib.ScopeOnce); !errors.Is(err, ErrCognitiveApprovalConflict) {
		t.Fatalf("expired approval: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if response := engine.WaitForApproval(ctx, expired); response.Decision != permissionlib.DecisionDeny {
		t.Fatalf("expired prompt did not fail closed: %+v", response)
	}
	approvals.Finish(expired.ID)
}
