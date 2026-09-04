package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// LoopRunStatusStore is the authoritative LoopRun projection used by the
// shared workflow-host bridge.
type LoopRunStatusStore interface {
	GetLoopRun(context.Context, string) (*store.LoopRun, error)
}

// LoopStepLauncher is the acyclic service-native child LoopRun launch seam.
type LoopStepLauncher interface {
	LaunchLoop(context.Context, LoopStepLaunchRequest) (LoopStepLaunchResult, error)
}

type LoopStepContinuationPolicy struct {
	Provider  string
	Model     string
	AgentID   string
	SessionID string
	Tools     []string
}

type LoopStepLaunchRequest struct {
	// IdempotencyKey is stable for one containing workflow node. The loop
	// implementation uses it to recover the same LoopRun after a crash
	// between child launch and outer-wait persistence.
	IdempotencyKey string
	WorkflowName   string
	GoalID         string

	GoalIntent             string
	GoalDesiredState       []string
	GoalConstraints        []string
	GoalAcceptanceCriteria []string
	GoalInvariants         []string
	GoalParentID           string
	GoalPriority           string
	GoalScope              string
	GoalOwner              string
	GoalSource             string

	Budget             store.Budget
	ContinuationPolicy LoopStepContinuationPolicy
	WorkflowParams     map[string]any
	AgentProfileID     string
	ProjectID          string
	ParentSessionID    string
	TimeoutSeconds     int
}

type LoopStepLaunchResult struct {
	LoopRunID        string
	Status           string
	CurrentIteration int
}

// LoopStepOuterStore locates the outer workflow node waiting on a LoopRun.
type LoopStepOuterStore interface {
	GetWorkflowRunStepByLoopRunID(context.Context, string) (*store.WorkflowRunStepRow, error)
}

// LoopResumeNotifier pushes a terminal child LoopRun back through the one
// durable WorkflowLauncher control surface.
type LoopResumeNotifier struct {
	store    LoopStepOuterStore
	launcher *WorkflowLauncher
}

func NewLoopResumeNotifier(st LoopStepOuterStore, launcher *WorkflowLauncher) *LoopResumeNotifier {
	return &LoopResumeNotifier{store: st, launcher: launcher}
}

func (n *LoopResumeNotifier) NotifyLoopRunTerminal(ctx context.Context, loopRunID string) error {
	if n == nil || n.store == nil || n.launcher == nil {
		return errors.New("loop resume notifier: not fully configured")
	}
	step, err := n.store.GetWorkflowRunStepByLoopRunID(ctx, loopRunID)
	if err != nil {
		if errors.Is(err, store.ErrWorkflowRunStepNotFound) {
			return nil
		}
		return fmt.Errorf("loop resume notifier: find outer step for loop_run %s: %w", loopRunID, err)
	}
	if step.Status != "waiting_on_loop" {
		return nil
	}
	if _, err := n.launcher.Resume(ctx, step.WorkflowRunID); err != nil {
		return fmt.Errorf("loop resume notifier: resume outer workflow_run %s: %w", step.WorkflowRunID, err)
	}
	return nil
}
