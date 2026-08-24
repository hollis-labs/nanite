package loop

// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md — the real
// push mechanism a StepKindLoop step's outer WorkflowRun needs: "when
// LoopEngine.Run/.Resume drives a LoopRun to a terminal state... it must
// itself call .Resume on the outer WorkflowRun that's waiting on it... a
// direct function call from internal/loop back into internal/service's
// resume path... not a lazy re-check" (that task's own Context section,
// quoted here since this file is the direct implementation of it).
//
// OuterResumeNotifier is declared here — the consumer (LoopEngine is what
// calls it) — mirroring internal/service/workflow_engine_flex.go's own
// "consumer declares the narrow interface" precedent one layer up
// (TeamMembershipStore/FlexStepStateCollector). Its real implementation,
// service.LoopResumeNotifier, lives in internal/service
// (workflow_engine_loop.go) and is wired in at container-build time via
// WithOuterResumeNotifier, below — NOT because a concrete
// *service.LoopResumeNotifier reference here would create an import
// cycle (it wouldn't: internal/loop already imports internal/service
// directly, for its own WorkflowLauncher dependency, task 08 — the
// interface exists for the same narrow-dependency-footprint reason
// TeamMembershipStore/FlexStepStateCollector exist, and so a LoopEngine
// built for a bare Manual/API-launched Loop (21-loops.md's "Trigger
// surface" — most LoopRuns are never launched from a StepKindLoop step at
// all) can simply leave this nil rather than being forced to thread
// through a notifier it has no outer run to ever call.
//
// See internal/service/workflow_engine_loop.go's own package doc comment
// for the actual, corrected cycle analysis: the real cycle risk this
// task's Context section flagged is in the OTHER direction — internal/
// service's own StepKindLoop executor calling LoopEngine.Run to LAUNCH a
// loop — handled by that file's own LoopStepLauncher interface, not this
// one.

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// OuterResumeNotifier is the push-mechanism surface a LoopEngine needs
// when launched to service a StepKindLoop step: once a LoopRun reaches a
// genuine terminal state, NotifyLoopRunTerminal tells whatever outer
// WorkflowRun is waiting on it (found via workflow_run_steps, by
// loop_run_id) to re-check itself — a real push, not the lazy per-Resume
// re-check flex steps use (see internal/service/workflow_engine.go's own
// comment on that precedent, quoted in this task's Context).
type OuterResumeNotifier interface {
	NotifyLoopRunTerminal(ctx context.Context, loopRunID string) error
}

// WithOuterResumeNotifier attaches n as this LoopEngine's push mechanism.
// Kept as a post-construction setter (not a NewLoopEngine parameter),
// mirroring internal/service's own WithFlexSupport/WithLoopSupport
// precedent: the many call sites that construct a LoopEngine for a bare
// Manual/API-launched Loop (this package's own engine_test.go included)
// don't need to thread through a dependency they have no use for. A
// LoopEngine constructed without one still runs Run/Resume exactly as
// before — see notifyOuterOnTerminal's own doc comment for what changes
// when it's set. Returns e for convenient chaining at the call site.
func (e *LoopEngine) WithOuterResumeNotifier(n OuterResumeNotifier) *LoopEngine {
	e.outerResume = n
	return e
}

// notifyOuterOnTerminal calls the configured OuterResumeNotifier once lr
// reaches a genuine terminal status (completed/failed/canceled). A no-op
// when no notifier is configured (the ordinary case for a Manual/API-
// launched Loop with no outer WorkflowRun waiting on it at all) or when
// status is not one of the three terminal values — waiting_on_gate/
// waiting_on_escalation are pauses, not terminal states, and a
// StepKindLoop step correctly stays waiting_on_loop through those (see
// service.recheckLoopStep).
//
// A notify failure is logged, not propagated: by the time this runs, lr's
// own terminal status is already durably persisted — this LoopRun's real
// outcome is correct regardless of whether the specific outer WorkflowRun
// waiting on it successfully got poked this call. Known limitation, and a
// documented follow-up candidate: there is no automatic retry of a failed
// push. An operator who notices a stuck waiting_on_loop run can always
// resume the outer WorkflowRun directly (the same GetEngine+concrete-
// engine-.Resume path the notifier itself uses) without waiting on a
// second LoopRun terminal transition that will never come.
func (e *LoopEngine) notifyOuterOnTerminal(ctx context.Context, loopRunID, status string) {
	if e.outerResume == nil {
		return
	}
	switch status {
	case store.LoopRunStatusCompleted, store.LoopRunStatusFailed, store.LoopRunStatusCanceled:
	default:
		return
	}
	if err := e.outerResume.NotifyLoopRunTerminal(ctx, loopRunID); err != nil {
		slog.Default().Error("loop: notify outer workflow run of terminal loop run failed",
			"loop_run_id", loopRunID, "status", status, "error", err)
	}
}

// Compile-time assertion that service.LoopResumeNotifier satisfies
// OuterResumeNotifier -- the symmetric counterpart to step_launcher.go's
// own `var _ service.LoopStepLauncher = (*LoopEngine)(nil)` assertion for
// the other direction. Legal here (not in internal/service) for the same
// reason step_launcher.go's own assertion lives in this package: this
// package already imports internal/service, so referencing its concrete
// type carries no cycle risk, while internal/service declaring the
// reverse would.
var _ OuterResumeNotifier = (*service.LoopResumeNotifier)(nil)
