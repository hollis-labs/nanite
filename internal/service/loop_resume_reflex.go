package service

// TASKS/loops/11-loop-event-predicate-trigger.md -- the resume_loop_run
// reflex action kind's real handler + evaluation entry point.
//
// Mirrors chat_reflex_dispatch.go's attemptReflexDispatch, and for the
// identical structural reason dispatch_to_agent needed a dedicated call
// site instead of running through internal/agent/reflexes.Engine.
// EvaluateState's generic per-turn pass: Executor.Apply's own
// resume_loop_run case (internal/agent/reflexes/executor.go) is a
// documented no-op -- the real effect (calling internal/loop.LoopEngine.
// Resume) needs a subsystem internal/agent/reflexes deliberately does not
// depend on. EvaluateLoopRunResumeReflexes below lists this LoopRun's own
// resume_loop_run candidates (Store.ListAgentReflexesForLoopRun), calls
// the SAME shared reflexes.Resolve() primitive every other real caller
// uses, and -- for whichever candidate(s) Resolve() selects -- calls a
// LoopRunResumer directly, exactly the way attemptReflexDispatch performs
// the real dispatch itself after Resolve() picks a winner.
//
// LoopRunResumer is declared here (the consumer), not a direct
// internal/loop import, because internal/loop already imports
// internal/service (task 08's WorkflowLauncher dependency) -- the reverse
// import here would cycle (internal/service -> internal/loop ->
// internal/service), the identical shape workflow_engine_loop.go's own
// LoopStepLauncher already resolves for the opposite (launch) direction.
// *loop.LoopEngine implements this interface via a thin ResumeLoopRun
// wrapper method (internal/loop/reflex_resume.go), legal there because
// internal/loop already imports internal/service. Wired together at
// container-build time (cmd/nanite/main.go), mirroring
// LoopStepLauncher/OuterResumeNotifier's own wiring.
//
// Evaluation-cadence resolution (this task's own "What to do" item 3,
// documented here since this file is where the resolution actually
// lives, not just in the task's Work Log): EvaluateLoopRunResumeReflexes
// is NOT invoked by Engine.EvaluateState's generic per-turn pass --
// engine.go filters resume_loop_run out of its own candidate list before
// Resolve() ever runs, for the same reason dispatch_to_agent is filtered
// (a no-op Apply() there would falsely register as a real fire). Beyond
// that structural exclusion, a resume_loop_run reflex has no live chat
// session to piggyback a per-turn pass on top of in the first place -- a
// LoopRun's waiting_on_escalation pause routinely spans zero live chat
// turns (it may have been launched headlessly, or its owning session may
// have ended long before the external condition becomes true). The real,
// sole evaluation cadence for this action kind is whatever explicitly
// calls this function against a still-waiting LoopRun -- in this
// codebase's actual design, that is task 12's scheduled loop_run_tick
// JobType (docs/engineering/architecture/21-loops.md's "Trigger surface"
// section: "a new loop_run_tick JobType ... for ... a WAIT-status loop
// polling an external condition"). This confirms, rather than merely
// repeats, that design doc's own suspicion: the "Event/predicate"
// trigger surface's *AST* is fully reused (EvaluateTrigger, unchanged),
// but its *firing mechanism* for this specific action kind is, in
// practice, a specialization consumed by the scheduled-tick surface, not
// an independently-sufficient cadence of its own.

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// LoopRunResumer is the narrow internal/loop.LoopEngine.Resume surface a
// resume_loop_run reflex's fired handler needs -- see this file's own
// package doc comment for the full import-cycle reasoning.
type LoopRunResumer interface {
	ResumeLoopRun(ctx context.Context, loopRunID string) error
}

// EvaluateLoopRunResumeReflexes evaluates every active resume_loop_run
// reflex scoped to loopRunID (action_spec.loop_run_id) against state, and
// calls resumer.ResumeLoopRun for whichever candidate(s) the shared
// reflexes.Resolve() decision primitive selects. Returns fired=true iff
// at least one candidate's action was actually applied (i.e. its trigger
// evaluated true and it survived the resume_loop_run kind's combining
// algorithm and cooldown check) -- this does not by itself mean
// ResumeLoopRun succeeded; a resume error is returned as err (the first
// one encountered) after every fired candidate has still been attempted,
// matching Resolve()'s own "one candidate's failure does not abort the
// rest of the pass" posture.
//
// Guarded to only proceed when loopRunID's own loop_runs.status is
// currently store.LoopRunStatusWaitingOnEscalation -- the status a
// DecisionWait (or DecisionEscalate) decision persists (internal/loop/
// engine.go's evaluateDecideAndAct). A LoopRun in
// LoopRunStatusWaitingOnGate is blocked on its current iteration's own
// inner WorkflowRun (a gate or flex step inside it), which an
// event/predicate resume_loop_run reflex was never meant to resolve --
// LoopEngine.Resume would still technically accept the call for that
// status (it dispatches to resumeBlockedIteration instead of
// driveIterations), but firing an external-condition reflex at an
// inner-gate pause would be resuming the wrong thing for the wrong
// reason. Any other status (already resumed past waiting, terminal, or
// waiting_on_gate) is a harmless no-op — fired=false, err=nil — since a
// scheduled caller may legitimately race against this LoopRun's own
// concurrent resolution (e.g. two ticks in flight, or a human resolving
// the escalation through a different path first).
func EvaluateLoopRunResumeReflexes(
	ctx context.Context,
	reflexEngine *reflexes.Engine,
	resumer LoopRunResumer,
	loopRunID string,
	state reflexes.State,
) (fired bool, err error) {
	if reflexEngine == nil || reflexEngine.Store == nil {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: reflex engine is not configured")
	}
	if resumer == nil {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: resumer is nil")
	}
	if loopRunID == "" {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: loop_run_id is required")
	}

	lr, lerr := reflexEngine.Store.GetLoopRun(ctx, loopRunID)
	if lerr != nil {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: get loop run %s: %w", loopRunID, lerr)
	}
	if lr.Status != store.LoopRunStatusWaitingOnEscalation {
		reflexEngine.Logger.Info("resume_loop_run: loop run is not waiting_on_escalation, skipping evaluation",
			"loop_run_id", loopRunID, "status", lr.Status)
		return false, nil
	}

	candidates, lerr := reflexEngine.Store.ListAgentReflexesForLoopRun(ctx, loopRunID)
	if lerr != nil {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: list candidates for loop run %s: %w", loopRunID, lerr)
	}
	if len(candidates) == 0 {
		return false, nil
	}

	// Facet 4 recurrence cascade, applied explicitly here for the same
	// reason attemptReflexDispatch applies it explicitly rather than
	// inheriting it from EvaluateState's loop (chat_reflex_dispatch.go's
	// own design note 2): this is a dedicated call site, not
	// EvaluateState. resume_loop_run's kind-level default is seeded 0 (no
	// cooldown, migration 142) -- a scheduled tick re-checking a
	// still-waiting LoopRun needs every invocation to genuinely
	// re-evaluate the trigger.
	now := time.Now()
	kindDefault := reflexEngine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionResumeLoopRun)
	cooldownFn := func(r store.AgentReflex) bool {
		cooldown := reflexes.EffectiveCooldown(kindDefault, r.RecurrenceOverrideSeconds)
		return reflexes.RecentlyFired(r, now, cooldown)
	}

	resumeLoopRunKind, kindErr := reflexEngine.Store.GetReflexActionKind(ctx, store.ReflexActionResumeLoopRun)
	kindLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
		return resumeLoopRunKind, kindErr
	}

	resolved, outcomes, resolveErr := reflexes.Resolve(ctx, candidates, state, reflexEngine.Executor, cooldownFn, kindLookup)
	if resolveErr != nil {
		return false, fmt.Errorf("EvaluateLoopRunResumeReflexes: resolve loop run %s: %w", loopRunID, resolveErr)
	}
	for _, oc := range outcomes {
		if oc.TriggerError != "" {
			reflexEngine.Logger.Warn("resume_loop_run: trigger evaluate failed",
				"loop_run_id", loopRunID, "reflex", oc.ReflexName, "err", oc.TriggerError)
		}
		if oc.ApplyError != "" {
			reflexEngine.Logger.Warn("resume_loop_run: apply failed",
				"loop_run_id", loopRunID, "reflex", oc.ReflexName, "err", oc.ApplyError)
		}
	}

	// Unified telemetry (TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md)
	// -- the same EmitFirings every other real Resolve() caller goes
	// through, so a resume_loop_run firing shows up in event_log/
	// fired_count/plugin hooks identically to every other action kind,
	// even though this call site (like attemptReflexDispatch) is
	// dedicated rather than routed through EvaluateState.
	reflexes.EmitFirings(ctx, reflexEngine.Store, reflexEngine.Plugins, resolved, outcomes, state, reflexes.FiringContext{
		AgentID:    state.AgentID,
		AgentClass: state.AgentClass,
	}, reflexEngine.Logger)

	if len(resolved.Actions) == 0 {
		return false, nil
	}

	var firstErr error
	for _, action := range resolved.Actions {
		targetLoopRunID, _ := action.Spec["loop_run_id"].(string)
		if targetLoopRunID == "" {
			// Defensive only -- Store.ListAgentReflexesForLoopRun only ever
			// returns rows whose action_spec.loop_run_id already equals
			// loopRunID (that's the query's own WHERE clause), and
			// validateReflexDefinition (internal/api/reflexes.go) rejects
			// an empty loop_run_id at write time for anything created
			// through the CRUD path.
			targetLoopRunID = loopRunID
		}
		if resumeErr := resumer.ResumeLoopRun(ctx, targetLoopRunID); resumeErr != nil {
			reflexEngine.Logger.Warn("resume_loop_run: LoopEngine.Resume failed",
				"loop_run_id", targetLoopRunID, "err", resumeErr)
			if firstErr == nil {
				firstErr = fmt.Errorf("resume loop run %s: %w", targetLoopRunID, resumeErr)
			}
			continue
		}
		reflexEngine.Logger.Info("resume_loop_run: LoopEngine.Resume called", "loop_run_id", targetLoopRunID)
	}

	return true, firstErr
}
