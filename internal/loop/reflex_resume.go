package loop

// TASKS/loops/11-loop-event-predicate-trigger.md -- the resume_loop_run
// reflex action kind's own handler needs to call LoopEngine.Resume. Same
// import-cycle shape as step_launcher.go's LaunchLoop (task 09's own
// "launch direction" fix), applied to the "resume direction": a
// resume_loop_run reflex fires inside internal/agent/reflexes'
// Resolve()/Executor.Apply, but that package cannot import internal/loop
// directly (internal/loop already imports internal/service, which
// imports internal/agent/reflexes -- a reflexes -> loop -> service ->
// reflexes cycle). The real caller is internal/service/
// loop_resume_reflex.go's EvaluateLoopRunResumeReflexes -- also in
// internal/service, which does not import internal/loop today (only
// cmd/nanite/main.go wires the two together, exactly as it already does
// for LoopStepLauncher/OuterResumeNotifier).
//
// service.LoopRunResumer is declared in internal/service (the consumer)
// speaking only a bare (ctx, loopRunID) -> error shape -- no
// internal/loop type crosses that boundary, mirroring LoopStepLauncher's
// own request/result translation discipline. This file makes *LoopEngine
// satisfy it structurally -- legal here because THIS package already
// imports internal/service, so this method can freely call Resume and
// discard its internal/loop-native LoopResult without creating the
// reverse edge.

import (
	"context"

	"github.com/hollis-labs/nanite/internal/service"
)

// ResumeLoopRun implements service.LoopRunResumer. Thin: calls Resume and
// discards the LoopResult -- a resume_loop_run reflex's handler only ever
// needs to know whether the resume attempt itself failed (surfaced as the
// returned error, logged by the caller), not the LoopRun's resulting
// status; a caller that wants the full LoopResult (e.g. a future operator
// UI) can call Resume directly instead of going through this narrower
// interface.
func (e *LoopEngine) ResumeLoopRun(ctx context.Context, loopRunID string) error {
	_, err := e.Resume(ctx, loopRunID)
	return err
}

// Compile-time assertion that *LoopEngine satisfies service.LoopRunResumer
// -- built proactively this time (not added later as a review fix, unlike
// step_launcher.go/outer_resume.go's own history) since both of those
// files' assertions are cheap, load-bearing insurance against a future
// signature drift going undetected.
var _ service.LoopRunResumer = (*LoopEngine)(nil)
