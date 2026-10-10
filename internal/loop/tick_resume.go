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
	"github.com/hollis-labs/nanite/internal/store"
)

// TickResumeBridge preserves the scheduler port. Mutable historical reflex
// resume is retired; no absent candidate or classification permits blind resume.
type TickResumeBridge struct {
	Engine       *LoopEngine
	ReflexEngine *reflexes.Engine
}

func NewTickResumeBridge(engine *LoopEngine, reflexEngine *reflexes.Engine) *TickResumeBridge {
	return &TickResumeBridge{Engine: engine, ReflexEngine: reflexEngine}
}

// Resume validates the transport inputs and returns the actual retired-policy
// refusal before reading or mutating a waiting loop. New pinned loop policies
// require a separately supported handler; historical rows cannot provide one.
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

	_, _, err := service.EvaluateLoopRunResumeReflexes(ctx, b.ReflexEngine, b.Engine, loopRunID, reflexes.State{})
	if err != nil {
		return LoopResult{}, fmt.Errorf("loop: tick resume bridge: %w", err)
	}
	// A future evaluator must not silently enable the historical fallback.
	return LoopResult{}, store.ErrImmutableAgentProfile
}
