package loop

// Integration fix for TASKS/loops/11-loop-event-predicate-trigger.md +
// 12-loop-run-tick-scheduled-trigger.md, per TASKS/ESCALATIONS.md's
// 2026-08-21 entry ("Phase 3's two parallel trigger tasks (11, 12) built
// compatible but disconnected mechanisms"): task 11 built the
// resume_loop_run reflex's real evaluation entry point
// (service.EvaluateLoopRunResumeReflexes), and task 11's own investigation
// correctly concluded its only real evaluation cadence is task 12's
// scheduled loop_run_tick -- but task 12 (built in a fully isolated,
// concurrent worktree that could not see task 11's final shape) wired
// internal/scheduler.RunnerAdapter.Loops directly to *LoopEngine, whose
// bare Resume(ctx, loopRunID) blind-resumes without ever calling
// EvaluateLoopRunResumeReflexes. Net effect before this file: a
// resume_loop_run reflex's own trigger-spec predicate was never actually
// evaluated by anything reachable from production wiring.
//
// TickResumeBridge closes that gap. It implements
// internal/scheduler.LoopResumer's exact shape --
// Resume(ctx, loopRunID string) (LoopResult, error) -- structurally, the
// same "consumer declares a narrow interface, satisfied structurally, wired
// at container-build time" pattern this batch already uses three times
// (LoopStepLauncher/OuterResumeNotifier, task 09; LoopRunResumer, task 11).
// internal/scheduler is NOT imported here (that would cycle:
// internal/scheduler already imports internal/loop) -- this file only ever
// needs to match that interface's method shape, not reference the
// interface type itself, exactly as reflex_resume.go's own
// service.LoopRunResumer satisfaction does for the opposite direction.
//
// Lives in internal/loop, not internal/service (where
// EvaluateLoopRunResumeReflexes is actually declared), because it is the
// one place that can see both *LoopEngine/LoopResult (needed to satisfy
// scheduler.LoopResumer's real return type) and
// internal/service.EvaluateLoopRunResumeReflexes/*reflexes.Engine
// (internal/loop already imports internal/service, task 08's
// WorkflowLauncher dependency). internal/agent/reflexes does not import
// internal/loop anywhere (confirmed directly: only comments in that
// package mention internal/loop, explaining why the reverse edge would
// cycle) -- so internal/loop importing internal/agent/reflexes directly,
// to build the reflexes.State this bridge passes through, is also safe.

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/service"
)

// TickResumeBridge wraps a *LoopEngine and a *reflexes.Engine so a scheduled
// loop_run_tick (internal/scheduler's enqueueLoopRunTick) resumes a WAITing
// LoopRun through the resume_loop_run reflex's real evaluation entry point
// first, falling back to a direct LoopEngine.Resume only when no such
// reflex is attached at all -- see this file's own package doc comment for
// the full background.
type TickResumeBridge struct {
	Engine       *LoopEngine
	ReflexEngine *reflexes.Engine
}

// NewTickResumeBridge constructs a TickResumeBridge. Both arguments are
// required -- Resume returns a clear error rather than panicking on a nil
// dependency, matching this codebase's own narrow-dependency convention
// (internal/scheduler.RunnerAdapter's own per-field nil checks).
func NewTickResumeBridge(engine *LoopEngine, reflexEngine *reflexes.Engine) *TickResumeBridge {
	return &TickResumeBridge{Engine: engine, ReflexEngine: reflexEngine}
}

// Resume implements internal/scheduler.LoopResumer. Called by
// enqueueLoopRunTick after it has already confirmed loopRunID's own
// loop_runs.status is resumable (waiting_on_gate or waiting_on_escalation)
// -- this method does not re-check that itself, matching task 12's own
// division of labor (the scheduler checks status before dispatching; the
// dispatch target trusts that check).
//
// Three real outcomes, driven entirely by
// service.EvaluateLoopRunResumeReflexes's (fired, hadCandidates, err)
// triple:
//
//  1. fired=true: a resume_loop_run reflex's trigger evaluated true this
//     call, and EvaluateLoopRunResumeReflexes already called
//     resumer.ResumeLoopRun (== Engine.ResumeLoopRun, i.e. Engine.Resume)
//     internally as part of resolving that firing. The resume already
//     happened -- this method only re-fetches the LoopRun's now-current row
//     to build the LoopResult the caller expects back, rather than calling
//     Resume a second time.
//  2. hadCandidates=true, fired=false: a resume_loop_run reflex IS attached
//     to this LoopRun, but its trigger has not fired yet. This is exactly
//     the case the pre-fix wiring got wrong -- blind-resuming here would
//     bypass the very predicate the reflex exists to enforce. This method
//     does NOT call Engine.Resume; it returns the LoopRun's current
//     (still-waiting) state untouched.
//  3. hadCandidates=false: no resume_loop_run reflex is attached to this
//     LoopRun at all -- the plain "durable preset, just retry on a timer"
//     case tick_schedule.go's own doc comment describes. Falls back to
//     calling Engine.Resume directly, preserving task 12's original default
//     behavior for exactly this case.
//
// A headless reflexes.State{} (no SessionID, no live chat window) is what
// this call site passes to EvaluateLoopRunResumeReflexes -- following task
// 11's own end-to-end test (reflex_resume_test.go)'s precedent for
// constructing one outside a live chat turn. Populating State.Events (or
// any other predicate-relevant signal) from a real external source is
// explicitly out of scope for this fix -- the same event/predicate
// trigger-spec AST (EvaluateTrigger, unchanged by this file) already
// evaluates whatever State it's given; wiring a live event feed into that
// State is a separate concern from making the resume_loop_run reflex path
// reachable at all, which is this file's entire job.
func (b *TickResumeBridge) Resume(ctx context.Context, loopRunID string) (LoopResult, error) {
	if b == nil || b.Engine == nil {
		return LoopResult{}, fmt.Errorf("loop: tick resume bridge: loop engine is not configured")
	}
	if b.ReflexEngine == nil {
		return LoopResult{}, fmt.Errorf("loop: tick resume bridge: reflex engine is not configured")
	}
	if loopRunID == "" {
		return LoopResult{}, fmt.Errorf("loop: tick resume bridge: loop_run_id is required")
	}

	fired, hadCandidates, err := service.EvaluateLoopRunResumeReflexes(ctx, b.ReflexEngine, b.Engine, loopRunID, reflexes.State{})
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: tick resume bridge: evaluate resume_loop_run reflexes for %s: %w", loopRunID, err)
	}

	if fired || hadCandidates {
		// Case 1 (fired): the resume already happened inside
		// EvaluateLoopRunResumeReflexes -- re-fetch, don't re-resume.
		// Case 2 (hadCandidates, not fired): a reflex is attached but its
		// trigger hasn't fired -- report the current (still-waiting) state,
		// and deliberately do NOT fall through to Engine.Resume below.
		lr, gerr := b.Engine.store.GetLoopRun(ctx, loopRunID)
		if gerr != nil {
			return LoopResult{}, fmt.Errorf("loop: tick resume bridge: get loop run %s: %w", loopRunID, gerr)
		}
		return LoopResult{LoopRunID: lr.ID, Status: lr.Status, CurrentIteration: lr.CurrentIteration}, nil
	}

	// Case 3: no resume_loop_run reflex attached at all -- fall back to
	// task 12's original default, a direct blind resume.
	return b.Engine.Resume(ctx, loopRunID)
}
